package v2

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	logsV4 "github.com/SigNoz/signoz/pkg/query-service/app/logs/v4"
	metricsV4 "github.com/SigNoz/signoz/pkg/query-service/app/metrics/v4"
	"github.com/SigNoz/signoz/pkg/query-service/app/queryBuilder"
	tracesV4 "github.com/SigNoz/signoz/pkg/query-service/app/traces/v4"
	"github.com/SigNoz/signoz/pkg/query-service/common"
	"github.com/SigNoz/signoz/pkg/query-service/constants"
	chErrors "github.com/SigNoz/signoz/pkg/query-service/errors"
	"github.com/SigNoz/signoz/pkg/query-service/querycache"
	"github.com/SigNoz/signoz/pkg/query-service/utils"
	"github.com/SigNoz/signoz/pkg/valuer"

	"github.com/SigNoz/signoz/pkg/cache"
	"github.com/SigNoz/signoz/pkg/query-service/interfaces"
	"github.com/SigNoz/signoz/pkg/query-service/model"
	v3 "github.com/SigNoz/signoz/pkg/query-service/model/v3"
	"go.uber.org/multierr"
	"go.uber.org/zap"
)

type channelResult struct {
	Series []*v3.Series
	List   []*v3.Row
	Err    error
	Name   string
	Query  string
}

type querier struct {
	cache        cache.Cache
	reader       interfaces.Reader
	keyGenerator cache.KeyGenerator
	queryCache   interfaces.QueryCache

	fluxInterval time.Duration

	builder *queryBuilder.QueryBuilder

	// used for testing
	// TODO(srikanthccv): remove this once we have a proper mock
	testingMode     bool
	queriesExecuted []string
	// tuple of start and end time in milliseconds
	timeRanges     [][]int
	returnedSeries []*v3.Series
	returnedErr    error
}

type QuerierOptions struct {
	Reader       interfaces.Reader
	Cache        cache.Cache
	KeyGenerator cache.KeyGenerator
	FluxInterval time.Duration

	// used for testing
	TestingMode    bool
	ReturnedSeries []*v3.Series
	ReturnedErr    error
}

func NewQuerier(opts QuerierOptions) interfaces.Querier {
	logsQueryBuilder := logsV4.PrepareLogsQuery
	tracesQueryBuilder := tracesV4.PrepareTracesQuery

	qc := querycache.NewQueryCache(querycache.WithCache(opts.Cache), querycache.WithFluxInterval(opts.FluxInterval))

	return &querier{
		cache:        opts.Cache,
		queryCache:   qc,
		reader:       opts.Reader,
		keyGenerator: opts.KeyGenerator,
		fluxInterval: opts.FluxInterval,

		builder: queryBuilder.NewQueryBuilder(queryBuilder.QueryBuilderOptions{
			BuildTraceQuery:  tracesQueryBuilder,
			BuildLogQuery:    logsQueryBuilder,
			BuildMetricQuery: metricsV4.PrepareMetricQuery,
		}),

		testingMode:    opts.TestingMode,
		returnedSeries: opts.ReturnedSeries,
		returnedErr:    opts.ReturnedErr,
	}
}

// execClickHouseQuery executes the clickhouse query and returns the series list
// if testing mode is enabled, it returns the mocked series list
func (q *querier) execClickHouseQuery(ctx context.Context, query string) ([]*v3.Series, error) {
	if q.testingMode && q.reader == nil {
		q.queriesExecuted = append(q.queriesExecuted, query)
		return q.returnedSeries, q.returnedErr
	}
	result, err := q.reader.GetTimeSeriesResultV3(ctx, query)
	var pointsWithNegativeTimestamps int
	// Filter out the points with negative or zero timestamps
	for idx := range result {
		series := result[idx]
		points := make([]v3.Point, 0)
		for pointIdx := range series.Points {
			point := series.Points[pointIdx]
			if point.Timestamp >= 0 {
				points = append(points, point)
			} else {
				pointsWithNegativeTimestamps++
			}
		}
		series.Points = points
	}
	if pointsWithNegativeTimestamps > 0 {
		zap.L().Error("found points with negative timestamps for query", zap.String("query", query))
	}
	return result, err
}

// execPromQuery executes the prom query and returns the series list
// if testing mode is enabled, it returns the mocked series list
func (q *querier) execPromQuery(ctx context.Context, params *model.QueryRangeParams) ([]*v3.Series, error) {
	if q.testingMode && q.reader == nil {
		q.queriesExecuted = append(q.queriesExecuted, params.Query)
		q.timeRanges = append(q.timeRanges, []int{int(params.Start.UnixMilli()), int(params.End.UnixMilli())})
		return q.returnedSeries, q.returnedErr
	}
	promResult, _, err := q.reader.GetQueryRangeResult(ctx, params)
	if err != nil {
		return nil, err
	}
	matrix, promErr := promResult.Matrix()
	if promErr != nil {
		return nil, promErr
	}
	var seriesList []*v3.Series
	for _, v := range matrix {
		var s v3.Series
		s.Labels = v.Metric.Copy().Map()
		for idx := range v.Floats {
			p := v.Floats[idx]
			s.Points = append(s.Points, v3.Point{Timestamp: p.T, Value: p.F})
		}
		seriesList = append(seriesList, &s)
	}
	return seriesList, nil
}

func (q *querier) runBuilderQueries(ctx context.Context, orgID valuer.UUID, params *v3.QueryRangeParamsV3) ([]*v3.Result, map[string]error, error) {

	cacheKeys := q.keyGenerator.GenerateKeys(params)

	now := time.Now()

	ch := make(chan channelResult, len(params.CompositeQuery.BuilderQueries))
	var wg sync.WaitGroup

	for queryName, builderQuery := range params.CompositeQuery.BuilderQueries {
		if queryName == builderQuery.Expression {
			wg.Add(1)
			go q.runBuilderQuery(ctx, orgID, builderQuery, params, cacheKeys, ch, &wg)
		}
	}

	wg.Wait()
	close(ch)
	zap.L().Info("time taken to run builder queries", zap.Duration("multiQueryDuration", time.Since(now)), zap.Int("num_queries", len(params.CompositeQuery.BuilderQueries)))

	results := make([]*v3.Result, 0)
	errQueriesByName := make(map[string]error)
	var errs []error

	for result := range ch {
		if result.Err != nil {
			errs = append(errs, result.Err)
			errQueriesByName[result.Name] = result.Err
			continue
		}
		results = append(results, &v3.Result{
			QueryName: result.Name,
			Series:    result.Series,
		})
	}

	var err error
	if len(errs) > 0 {
		err = fmt.Errorf("error in builder queries")
	}

	return results, errQueriesByName, err
}

func (q *querier) runPromQueries(ctx context.Context, orgID valuer.UUID, params *v3.QueryRangeParamsV3) ([]*v3.Result, map[string]error, error) {
	channelResults := make(chan channelResult, len(params.CompositeQuery.PromQueries))
	var wg sync.WaitGroup
	cacheKeys := q.keyGenerator.GenerateKeys(params)

	for queryName, promQuery := range params.CompositeQuery.PromQueries {
		if promQuery.Disabled {
			continue
		}
		wg.Add(1)
		go func(queryName string, promQuery *v3.PromQuery) {
			defer wg.Done()
			cacheKey, ok := cacheKeys[queryName]

			if !ok || params.NoCache {
				zap.L().Info("skipping cache for metrics prom query", zap.String("queryName", queryName), zap.Int64("start", params.Start), zap.Int64("end", params.End), zap.Int64("step", params.Step), zap.Bool("noCache", params.NoCache), zap.String("cacheKey", cacheKeys[queryName]))
				query := metricsV4.BuildPromQuery(promQuery, params.Step, params.Start, params.End)
				series, err := q.execPromQuery(ctx, query)
				channelResults <- channelResult{Err: err, Name: queryName, Query: query.Query, Series: series}
				return
			}
			misses := q.queryCache.FindMissingTimeRanges(orgID, params.Start, params.End, params.Step, cacheKey)
			zap.L().Info("cache misses for metrics prom query", zap.Any("misses", misses))
			missedSeries := make([]querycache.CachedSeriesData, 0)
			for _, miss := range misses {
				query := metricsV4.BuildPromQuery(promQuery, params.Step, miss.Start, miss.End)
				series, err := q.execPromQuery(ctx, query)
				if err != nil {
					channelResults <- channelResult{Err: err, Name: queryName, Query: query.Query, Series: nil}
					return
				}
				missedSeries = append(missedSeries, querycache.CachedSeriesData{
					Data:  series,
					Start: miss.Start,
					End:   miss.End,
				})
			}
			mergedSeries := q.queryCache.MergeWithCachedSeriesData(orgID, cacheKey, missedSeries)
			resultSeries := common.GetSeriesFromCachedData(mergedSeries, params.Start, params.End)
			channelResults <- channelResult{Err: nil, Name: queryName, Query: promQuery.Query, Series: resultSeries}
		}(queryName, promQuery)
	}
	wg.Wait()
	close(channelResults)

	results := make([]*v3.Result, 0)
	errQueriesByName := make(map[string]error)
	var errs []error

	for result := range channelResults {
		if result.Err != nil {
			errs = append(errs, result.Err)
			errQueriesByName[result.Name] = result.Err
			continue
		}
		results = append(results, &v3.Result{
			QueryName: result.Name,
			Series:    result.Series,
		})
	}

	var err error
	if len(errs) > 0 {
		err = fmt.Errorf("error in prom queries")
	}

	return results, errQueriesByName, err
}

func (q *querier) runClickHouseQueries(ctx context.Context, params *v3.QueryRangeParamsV3) ([]*v3.Result, map[string]error, error) {
	channelResults := make(chan channelResult, len(params.CompositeQuery.ClickHouseQueries))
	var wg sync.WaitGroup
	for queryName, clickHouseQuery := range params.CompositeQuery.ClickHouseQueries {
		if clickHouseQuery.Disabled {
			continue
		}
		wg.Add(1)
		go func(queryName string, clickHouseQuery *v3.ClickHouseQuery) {
			defer wg.Done()
			series, err := q.execClickHouseQuery(ctx, clickHouseQuery.Query)
			channelResults <- channelResult{Err: err, Name: queryName, Query: clickHouseQuery.Query, Series: series}
		}(queryName, clickHouseQuery)
	}
	wg.Wait()
	close(channelResults)

	results := make([]*v3.Result, 0)
	errQueriesByName := make(map[string]error)
	var errs []error

	for result := range channelResults {
		if result.Err != nil {
			errs = append(errs, result.Err)
			errQueriesByName[result.Name] = result.Err
			continue
		}
		results = append(results, &v3.Result{
			QueryName: result.Name,
			Series:    result.Series,
		})
	}

	var err error
	if len(errs) > 0 {
		err = fmt.Errorf("error in clickhouse queries")
	}
	return results, errQueriesByName, err
}

func (q *querier) runWindowBasedListQuery(ctx context.Context, params *v3.QueryRangeParamsV3, tsRanges []utils.LogsListTsRange) ([]*v3.Result, map[string]error, error) {
	res := make([]*v3.Result, 0)
	qName := ""
	pageSize := uint64(0)
	limit := uint64(0)
	offset := uint64(0)

	// Get query details and check order direction
	for name, v := range params.CompositeQuery.BuilderQueries {
		qName = name
		pageSize = v.PageSize
		limit = v.Limit
		offset = v.Offset

	}

	// Check if order is ascending
	if strings.ToLower(string(params.CompositeQuery.BuilderQueries[qName].OrderBy[0].Order)) == "asc" {
		// Reverse the time ranges for ascending order
		for i, j := 0, len(tsRanges)-1; i < j; i, j = i+1, j-1 {
			tsRanges[i], tsRanges[j] = tsRanges[j], tsRanges[i]
		}
	}

	// check if it is a logs query
	isLogs := false
	if params.CompositeQuery.BuilderQueries[qName].DataSource == v3.DataSourceLogs {
		isLogs = true
	}

	data := []*v3.Row{}

	limitWithOffset := limit + offset
	if isLogs {
		// for logs we use pageSize to define the current limit and limit to define the absolute limit
		limitWithOffset = pageSize + offset
		if limit > 0 && offset >= limit {
			return nil, nil, fmt.Errorf("max limit exceeded")
		}
	}

	for _, v := range tsRanges {
		params.Start = v.Start
		params.End = v.End
		length := uint64(0)

		// max limit + offset is 10k for pagination for traces/logs
		// TODO(nitya): define something for logs
		if !isLogs && limitWithOffset > constants.TRACE_V4_MAX_PAGINATION_LIMIT {
			return nil, nil, fmt.Errorf("maximum traces that can be paginated is 10000")
		}

		// we are updating the offset and limit based on the number of traces/logs we have found in the current timerange
		// eg -
		// 1)offset = 0, limit = 100, tsRanges = [t1, t10], [t10, 20], [t20, t30]
		//
		// if 100 traces/logs are there in [t1, t10] then 100 will return immediately.
		// if 10 traces/logs are there in [t1, t10] then we get 10, set offset to 0 and limit to 90, search in the next timerange of [t10, 20]
		// if we don't find any trace in [t1, t10], then we search in [t10, 20] with offset=0, limit=100

		//
		// 2) offset = 50, limit = 100, tsRanges = [t1, t10], [t10, 20], [t20, t30]
		//
		// If we find 150 traces/logs with limit=150 and offset=0 in [t1, t10] then we return immediately 100 traces/logs
		// If we find 50 in [t1, t10] with limit=150 and offset=0 then it will set limit = 100 and offset=0 and search in the next timerange of [t10, 20]
		// if we don't find any trace in [t1, t10], then we search in [t10, 20] with limit=150 and offset=0

		params.CompositeQuery.BuilderQueries[qName].Offset = 0
		// if datasource is logs
		if isLogs {
			// for logs we use limit to define the absolute limit and pagesize to define the current limit
			params.CompositeQuery.BuilderQueries[qName].PageSize = limitWithOffset
		} else {
			params.CompositeQuery.BuilderQueries[qName].Limit = limitWithOffset
		}

		queries, err := q.builder.PrepareQueries(params)
		if err != nil {
			return nil, nil, err
		}
		for name, query := range queries {
			rowList, err := q.reader.GetListResultV3(ctx, query)
			if err != nil {
				errs := []error{err}
				errQueriesByName := map[string]error{
					name: err,
				}
				return nil, errQueriesByName, fmt.Errorf("encountered multiple errors: %s", multierr.Combine(errs...))
			}
			length += uint64(len(rowList))

			// skip the traces unless offset is 0
			for _, row := range rowList {
				if offset == 0 {
					data = append(data, row)
				} else {
					offset--
				}
			}
		}

		limitWithOffset = limitWithOffset - length

		if isLogs && uint64(len(data)) >= pageSize {
			// for logs
			break
		} else if !isLogs && uint64(len(data)) >= limit {
			// for traces
			break
		}
	}
	res = append(res, &v3.Result{
		QueryName: qName,
		List:      data,
	})
	return res, nil, nil
}

func (q *querier) runBuilderListQueries(ctx context.Context, params *v3.QueryRangeParamsV3) ([]*v3.Result, map[string]error, error) {
	// List query has support for only one query.
	// we are skipping for PanelTypeTrace as it has a custom order by regardless of what's in the payload
	if params.CompositeQuery != nil &&
		len(params.CompositeQuery.BuilderQueries) == 1 &&
		params.CompositeQuery.PanelType != v3.PanelTypeTrace {
		for _, v := range params.CompositeQuery.BuilderQueries {

			// for logs: allow only when order of timestamp and id is same
			if v.DataSource == v3.DataSourceTraces &&
				len(v.OrderBy) == 1 &&
				v.OrderBy[0].ColumnName == "timestamp" {
				startEndArr := utils.GetListTsRanges(params.Start, params.End)
				return q.runWindowBasedListQuery(ctx, params, startEndArr)
			} else if v.DataSource == v3.DataSourceLogs &&
				len(v.OrderBy) == 2 &&
				v.OrderBy[0].ColumnName == "timestamp" &&
				v.OrderBy[1].ColumnName == "id" &&
				v.OrderBy[1].Order == v.OrderBy[0].Order {
				startEndArr := utils.GetListTsRanges(params.Start, params.End)
				return q.runWindowBasedListQuery(ctx, params, startEndArr)
			}
		}
	}

	queries := make(map[string]string)
	var err error
	if params.CompositeQuery.QueryType == v3.QueryTypeBuilder {
		queries, err = q.builder.PrepareQueries(params)
	} else if params.CompositeQuery.QueryType == v3.QueryTypeClickHouseSQL {
		for name, chQuery := range params.CompositeQuery.ClickHouseQueries {
			queries[name] = chQuery.Query
		}
	}

	if err != nil {
		return nil, nil, err
	}

	ch := make(chan channelResult, len(queries))
	var wg sync.WaitGroup

	for name, query := range queries {
		wg.Add(1)
		go func(name, query string) {
			defer wg.Done()
			rowList, err := q.reader.GetListResultV3(ctx, query)

			if err != nil {
				ch <- channelResult{Err: err, Name: name, Query: query}
				return
			}
			ch <- channelResult{List: rowList, Name: name, Query: query}
		}(name, query)
	}

	wg.Wait()
	close(ch)

	var errs []error
	errQueriesByName := make(map[string]error)
	res := make([]*v3.Result, 0)
	// read values from the channel
	for r := range ch {
		if r.Err != nil {
			errs = append(errs, r.Err)
			errQueriesByName[r.Name] = r.Err
			continue
		}
		res = append(res, &v3.Result{
			QueryName: r.Name,
			List:      r.List,
		})
	}
	if len(errs) != 0 {
		return nil, errQueriesByName, fmt.Errorf("encountered multiple errors: %s", multierr.Combine(errs...))
	}
	return res, nil, nil
}

// QueryRange is the main function that runs the queries
// and returns the results
func (q *querier) QueryRange(ctx context.Context, orgID valuer.UUID, params *v3.QueryRangeParamsV3) ([]*v3.Result, map[string]error, error) {
	var results []*v3.Result
	var err error
	var errQueriesByName map[string]error
	if !q.testingMode && q.reader != nil {
		q.ValidateMetricNames(ctx, params.CompositeQuery, orgID)
	}
	if params.CompositeQuery != nil {
		switch params.CompositeQuery.QueryType {
		case v3.QueryTypeBuilder:
			if params.CompositeQuery.PanelType == v3.PanelTypeList || params.CompositeQuery.PanelType == v3.PanelTypeTrace {
				results, errQueriesByName, err = q.runBuilderListQueries(ctx, params)
			} else {
				results, errQueriesByName, err = q.runBuilderQueries(ctx, orgID, params)
			}
			// in builder query, the only errors we expose are the ones that exceed the resource limits
			// everything else is internal error as they are not actionable by the user
			for name, err := range errQueriesByName {
				if !chErrors.IsResourceLimitError(err) {
					delete(errQueriesByName, name)
				}
			}
		case v3.QueryTypePromQL:
			results, errQueriesByName, err = q.runPromQueries(ctx, orgID, params)
		case v3.QueryTypeClickHouseSQL:
			if params.CompositeQuery.PanelType == v3.PanelTypeList || params.CompositeQuery.PanelType == v3.PanelTypeTrace {
				results, errQueriesByName, err = q.runBuilderListQueries(ctx, params)
			} else {
				ctx = context.WithValue(ctx, "enforce_max_result_rows", true)
				results, errQueriesByName, err = q.runClickHouseQueries(ctx, params)
			}
		default:
			err = fmt.Errorf("invalid query type")
		}
	}

	// return error if the number of series is more than one for value type panel
	if params.CompositeQuery.PanelType == v3.PanelTypeValue {
		if len(results) > 1 && params.CompositeQuery.EnabledQueries() > 1 {
			err = fmt.Errorf("there can be only one active query for value type panel")
		} else if len(results) == 1 && len(results[0].Series) > 1 {
			err = fmt.Errorf("there can be only one result series for value type panel but got %d", len(results[0].Series))
		}
	}

	return results, errQueriesByName, err
}

// QueriesExecuted returns the list of queries executed
// in the last query range call
// used for testing
func (q *querier) QueriesExecuted() []string {
	return q.queriesExecuted
}

// TimeRanges returns the list of time ranges
// that were used to fetch the data
// used for testing
func (q *querier) TimeRanges() [][]int {
	return q.timeRanges
}
