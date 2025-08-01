package inframetrics

import (
	"context"
	"math"
	"sort"

	"github.com/SigNoz/signoz/pkg/query-service/app/metrics/v4/helpers"
	"github.com/SigNoz/signoz/pkg/query-service/common"
	"github.com/SigNoz/signoz/pkg/query-service/interfaces"
	"github.com/SigNoz/signoz/pkg/query-service/model"
	v3 "github.com/SigNoz/signoz/pkg/query-service/model/v3"
	"github.com/SigNoz/signoz/pkg/query-service/postprocess"
	"github.com/SigNoz/signoz/pkg/valuer"
	"golang.org/x/exp/slices"
)

var (
	metricToUseForDaemonSets = GetDotMetrics("k8s_pod_cpu_usage")
	k8sDaemonSetNameAttrKey  = GetDotMetrics("k8s_daemonset_name")

	metricNamesForDaemonSets = map[string]string{
		"desired_nodes":   GetDotMetrics("k8s_daemonset_desired_scheduled_nodes"),
		"available_nodes": GetDotMetrics("k8s_daemonset_current_scheduled_nodes"),
	}

	daemonSetAttrsToEnrich = []string{
		GetDotMetrics("k8s_daemonset_name"),
		GetDotMetrics("k8s_namespace_name"),
		GetDotMetrics("k8s_cluster_name"),
	}

	queryNamesForDaemonSets = map[string][]string{
		"cpu":             {"A"},
		"cpu_request":     {"B", "A"},
		"cpu_limit":       {"C", "A"},
		"memory":          {"D"},
		"memory_request":  {"E", "D"},
		"memory_limit":    {"F", "D"},
		"restarts":        {"G", "A"},
		"desired_nodes":   {"H"},
		"available_nodes": {"I"},
	}

	builderQueriesForDaemonSets = map[string]*v3.BuilderQuery{
		// desired nodes
		"H": {
			QueryName:  "H",
			DataSource: v3.DataSourceMetrics,
			AggregateAttribute: v3.AttributeKey{
				Key:      metricNamesForDaemonSets["desired_nodes"],
				DataType: v3.AttributeKeyDataTypeFloat64,
			},
			Temporality: v3.Unspecified,
			Filters: &v3.FilterSet{
				Operator: "AND",
				Items:    []v3.FilterItem{},
			},
			GroupBy:          []v3.AttributeKey{},
			Expression:       "H",
			ReduceTo:         v3.ReduceToOperatorLast,
			TimeAggregation:  v3.TimeAggregationAnyLast,
			SpaceAggregation: v3.SpaceAggregationSum,
			Disabled:         false,
		},
		// available nodes
		"I": {
			QueryName:  "I",
			DataSource: v3.DataSourceMetrics,
			AggregateAttribute: v3.AttributeKey{
				Key:      metricNamesForDaemonSets["available_nodes"],
				DataType: v3.AttributeKeyDataTypeFloat64,
			},
			Temporality: v3.Unspecified,
			Filters: &v3.FilterSet{
				Operator: "AND",
				Items:    []v3.FilterItem{},
			},
			GroupBy:          []v3.AttributeKey{},
			Expression:       "I",
			ReduceTo:         v3.ReduceToOperatorLast,
			TimeAggregation:  v3.TimeAggregationAnyLast,
			SpaceAggregation: v3.SpaceAggregationSum,
			Disabled:         false,
		},
	}

	daemonSetQueryNames = []string{"A", "B", "C", "D", "E", "F", "G", "H", "I"}
)

type DaemonSetsRepo struct {
	reader    interfaces.Reader
	querierV2 interfaces.Querier
}

func NewDaemonSetsRepo(reader interfaces.Reader, querierV2 interfaces.Querier) *DaemonSetsRepo {
	return &DaemonSetsRepo{reader: reader, querierV2: querierV2}
}

func (d *DaemonSetsRepo) GetDaemonSetAttributeKeys(ctx context.Context, req v3.FilterAttributeKeyRequest) (*v3.FilterAttributeKeyResponse, error) {
	// TODO(srikanthccv): remove hardcoded metric name and support keys from any pod metric
	req.DataSource = v3.DataSourceMetrics
	req.AggregateAttribute = metricToUseForDaemonSets
	if req.Limit == 0 {
		req.Limit = 50
	}

	attributeKeysResponse, err := d.reader.GetMetricAttributeKeys(ctx, &req)
	if err != nil {
		return nil, err
	}

	// TODO(srikanthccv): only return resource attributes when we have a way to
	// distinguish between resource attributes and other attributes.
	filteredKeys := []v3.AttributeKey{}
	for _, key := range attributeKeysResponse.AttributeKeys {
		if slices.Contains(pointAttrsToIgnore, key.Key) {
			continue
		}
		filteredKeys = append(filteredKeys, key)
	}

	return &v3.FilterAttributeKeyResponse{AttributeKeys: filteredKeys}, nil
}

func (d *DaemonSetsRepo) GetDaemonSetAttributeValues(ctx context.Context, req v3.FilterAttributeValueRequest) (*v3.FilterAttributeValueResponse, error) {
	req.DataSource = v3.DataSourceMetrics
	req.AggregateAttribute = metricToUseForDaemonSets
	if req.Limit == 0 {
		req.Limit = 50
	}

	attributeValuesResponse, err := d.reader.GetMetricAttributeValues(ctx, &req)
	if err != nil {
		return nil, err
	}

	return attributeValuesResponse, nil
}

func (d *DaemonSetsRepo) getMetadataAttributes(ctx context.Context, req model.DaemonSetListRequest) (map[string]map[string]string, error) {
	daemonSetAttrs := map[string]map[string]string{}

	for _, key := range daemonSetAttrsToEnrich {
		hasKey := false
		for _, groupByKey := range req.GroupBy {
			if groupByKey.Key == key {
				hasKey = true
				break
			}
		}
		if !hasKey {
			req.GroupBy = append(req.GroupBy, v3.AttributeKey{Key: key})
		}
	}

	mq := v3.BuilderQuery{
		DataSource: v3.DataSourceMetrics,
		AggregateAttribute: v3.AttributeKey{
			Key:      metricToUseForDaemonSets,
			DataType: v3.AttributeKeyDataTypeFloat64,
		},
		Temporality: v3.Unspecified,
		GroupBy:     req.GroupBy,
	}

	query, err := helpers.PrepareTimeseriesFilterQuery(req.Start, req.End, &mq)
	if err != nil {
		return nil, err
	}

	query = localQueryToDistributedQuery(query)

	attrsListResponse, err := d.reader.GetListResultV3(ctx, query)
	if err != nil {
		return nil, err
	}

	for _, row := range attrsListResponse {
		stringData := map[string]string{}
		for key, value := range row.Data {
			if str, ok := value.(string); ok {
				stringData[key] = str
			} else if strPtr, ok := value.(*string); ok {
				stringData[key] = *strPtr
			}
		}

		daemonSetName := stringData[k8sDaemonSetNameAttrKey]
		if _, ok := daemonSetAttrs[daemonSetName]; !ok {
			daemonSetAttrs[daemonSetName] = map[string]string{}
		}

		for _, key := range req.GroupBy {
			daemonSetAttrs[daemonSetName][key.Key] = stringData[key.Key]
		}
	}

	return daemonSetAttrs, nil
}

func (d *DaemonSetsRepo) getTopDaemonSetGroups(ctx context.Context, orgID valuer.UUID, req model.DaemonSetListRequest, q *v3.QueryRangeParamsV3) ([]map[string]string, []map[string]string, error) {
	step, timeSeriesTableName, samplesTableName := getParamsForTopDaemonSets(req)

	queryNames := queryNamesForDaemonSets[req.OrderBy.ColumnName]
	topDaemonSetGroupsQueryRangeParams := &v3.QueryRangeParamsV3{
		Start: req.Start,
		End:   req.End,
		Step:  step,
		CompositeQuery: &v3.CompositeQuery{
			BuilderQueries: map[string]*v3.BuilderQuery{},
			QueryType:      v3.QueryTypeBuilder,
			PanelType:      v3.PanelTypeTable,
		},
	}

	for _, queryName := range queryNames {
		query := q.CompositeQuery.BuilderQueries[queryName].Clone()
		query.StepInterval = step
		query.MetricTableHints = &v3.MetricTableHints{
			TimeSeriesTableName: timeSeriesTableName,
			SamplesTableName:    samplesTableName,
		}
		if req.Filters != nil && len(req.Filters.Items) > 0 {
			if query.Filters == nil {
				query.Filters = &v3.FilterSet{Operator: "AND", Items: []v3.FilterItem{}}
			}
			query.Filters.Items = append(query.Filters.Items, req.Filters.Items...)
		}
		topDaemonSetGroupsQueryRangeParams.CompositeQuery.BuilderQueries[queryName] = query
	}

	queryResponse, _, err := d.querierV2.QueryRange(ctx, orgID, topDaemonSetGroupsQueryRangeParams)
	if err != nil {
		return nil, nil, err
	}
	formattedResponse, err := postprocess.PostProcessResult(queryResponse, topDaemonSetGroupsQueryRangeParams)
	if err != nil {
		return nil, nil, err
	}

	if len(formattedResponse) == 0 || len(formattedResponse[0].Series) == 0 {
		return nil, nil, nil
	}

	if req.OrderBy.Order == v3.DirectionDesc {
		sort.Slice(formattedResponse[0].Series, func(i, j int) bool {
			return formattedResponse[0].Series[i].Points[0].Value > formattedResponse[0].Series[j].Points[0].Value
		})
	} else {
		sort.Slice(formattedResponse[0].Series, func(i, j int) bool {
			return formattedResponse[0].Series[i].Points[0].Value < formattedResponse[0].Series[j].Points[0].Value
		})
	}

	limit := math.Min(float64(req.Offset+req.Limit), float64(len(formattedResponse[0].Series)))

	paginatedTopDaemonSetGroupsSeries := formattedResponse[0].Series[req.Offset:int(limit)]

	topDaemonSetGroups := []map[string]string{}
	for _, series := range paginatedTopDaemonSetGroupsSeries {
		topDaemonSetGroups = append(topDaemonSetGroups, series.Labels)
	}
	allDaemonSetGroups := []map[string]string{}
	for _, series := range formattedResponse[0].Series {
		allDaemonSetGroups = append(allDaemonSetGroups, series.Labels)
	}

	return topDaemonSetGroups, allDaemonSetGroups, nil
}

func (d *DaemonSetsRepo) GetDaemonSetList(ctx context.Context, orgID valuer.UUID, req model.DaemonSetListRequest) (model.DaemonSetListResponse, error) {
	resp := model.DaemonSetListResponse{}

	if req.Limit == 0 {
		req.Limit = 10
	}

	if req.OrderBy == nil {
		req.OrderBy = &v3.OrderBy{ColumnName: "cpu", Order: v3.DirectionDesc}
	}

	if req.GroupBy == nil {
		req.GroupBy = []v3.AttributeKey{{Key: k8sDaemonSetNameAttrKey}}
		resp.Type = model.ResponseTypeList
	} else {
		resp.Type = model.ResponseTypeGroupedList
	}

	step := int64(math.Max(float64(common.MinAllowedStepInterval(req.Start, req.End)), 60))

	query := WorkloadTableListQuery.Clone()

	query.Start = req.Start
	query.End = req.End
	query.Step = step

	// add additional queries for daemon sets
	for _, daemonSetQuery := range builderQueriesForDaemonSets {
		query.CompositeQuery.BuilderQueries[daemonSetQuery.QueryName] = daemonSetQuery.Clone()
	}

	for _, query := range query.CompositeQuery.BuilderQueries {
		query.StepInterval = step
		if req.Filters != nil && len(req.Filters.Items) > 0 {
			if query.Filters == nil {
				query.Filters = &v3.FilterSet{Operator: "AND", Items: []v3.FilterItem{}}
			}
			query.Filters.Items = append(query.Filters.Items, req.Filters.Items...)
		}
		query.GroupBy = req.GroupBy
		// make sure we only get records for daemon sets
		query.Filters.Items = append(query.Filters.Items, v3.FilterItem{
			Key:      v3.AttributeKey{Key: k8sDaemonSetNameAttrKey},
			Operator: v3.FilterOperatorExists,
		})
	}

	daemonSetAttrs, err := d.getMetadataAttributes(ctx, req)
	if err != nil {
		return resp, err
	}

	topDaemonSetGroups, allDaemonSetGroups, err := d.getTopDaemonSetGroups(ctx, orgID, req, query)
	if err != nil {
		return resp, err
	}

	groupFilters := map[string][]string{}
	for _, topDaemonSetGroup := range topDaemonSetGroups {
		for k, v := range topDaemonSetGroup {
			groupFilters[k] = append(groupFilters[k], v)
		}
	}

	for groupKey, groupValues := range groupFilters {
		hasGroupFilter := false
		if req.Filters != nil && len(req.Filters.Items) > 0 {
			for _, filter := range req.Filters.Items {
				if filter.Key.Key == groupKey {
					hasGroupFilter = true
					break
				}
			}
		}

		if !hasGroupFilter {
			for _, query := range query.CompositeQuery.BuilderQueries {
				query.Filters.Items = append(query.Filters.Items, v3.FilterItem{
					Key:      v3.AttributeKey{Key: groupKey},
					Value:    groupValues,
					Operator: v3.FilterOperatorIn,
				})
			}
		}
	}

	queryResponse, _, err := d.querierV2.QueryRange(ctx, orgID, query)
	if err != nil {
		return resp, err
	}

	formattedResponse, err := postprocess.PostProcessResult(queryResponse, query)
	if err != nil {
		return resp, err
	}

	records := []model.DaemonSetListRecord{}

	for _, result := range formattedResponse {
		for _, row := range result.Table.Rows {

			record := model.DaemonSetListRecord{
				DaemonSetName:  "",
				CPUUsage:       -1,
				CPURequest:     -1,
				CPULimit:       -1,
				MemoryUsage:    -1,
				MemoryRequest:  -1,
				MemoryLimit:    -1,
				DesiredNodes:   -1,
				AvailableNodes: -1,
			}

			if daemonSetName, ok := row.Data[k8sDaemonSetNameAttrKey].(string); ok {
				record.DaemonSetName = daemonSetName
			}

			if cpu, ok := row.Data["A"].(float64); ok {
				record.CPUUsage = cpu
			}
			if cpuRequest, ok := row.Data["B"].(float64); ok {
				record.CPURequest = cpuRequest
			}

			if cpuLimit, ok := row.Data["C"].(float64); ok {
				record.CPULimit = cpuLimit
			}

			if memory, ok := row.Data["D"].(float64); ok {
				record.MemoryUsage = memory
			}

			if memoryRequest, ok := row.Data["E"].(float64); ok {
				record.MemoryRequest = memoryRequest
			}

			if memoryLimit, ok := row.Data["F"].(float64); ok {
				record.MemoryLimit = memoryLimit
			}

			if restarts, ok := row.Data["G"].(float64); ok {
				record.Restarts = int(restarts)
			}

			if desiredNodes, ok := row.Data["H"].(float64); ok {
				record.DesiredNodes = int(desiredNodes)
			}

			if availableNodes, ok := row.Data["I"].(float64); ok {
				record.AvailableNodes = int(availableNodes)
			}

			record.Meta = map[string]string{}
			if _, ok := daemonSetAttrs[record.DaemonSetName]; ok && record.DaemonSetName != "" {
				record.Meta = daemonSetAttrs[record.DaemonSetName]
			}

			for k, v := range row.Data {
				if slices.Contains(daemonSetQueryNames, k) {
					continue
				}
				if labelValue, ok := v.(string); ok {
					record.Meta[k] = labelValue
				}
			}

			records = append(records, record)
		}
	}
	resp.Total = len(allDaemonSetGroups)
	resp.Records = records

	resp.SortBy(req.OrderBy)

	return resp, nil
}
