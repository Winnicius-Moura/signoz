import './MetricsAggregateSection.styles.scss';

import { Tooltip } from 'antd';
import cx from 'classnames';
import InputWithLabel from 'components/InputWithLabel/InputWithLabel';
import { ATTRIBUTE_TYPES, PANEL_TYPES } from 'constants/queryBuilder';
import SpaceAggregationOptions from 'container/QueryBuilder/components/SpaceAggregationOptions/SpaceAggregationOptions';
import { GroupByFilter, OperatorsSelect } from 'container/QueryBuilder/filters';
import { useQueryOperations } from 'hooks/queryBuilder/useQueryBuilderOperations';
import { Info } from 'lucide-react';
import { memo, useCallback, useEffect, useMemo } from 'react';
import { IBuilderQuery } from 'types/api/queryBuilder/queryBuilderData';
import { MetricAggregation } from 'types/api/v5/queryRange';

import { useQueryBuilderV2Context } from '../../QueryBuilderV2Context';

const MetricsAggregateSection = memo(function MetricsAggregateSection({
	query,
	index,
	version,
	panelType,
}: {
	query: IBuilderQuery;
	index: number;
	version: string;
	panelType: PANEL_TYPES | null;
}): JSX.Element {
	const { setAggregationOptions } = useQueryBuilderV2Context();
	const {
		operators,
		spaceAggregationOptions,
		handleChangeQueryData,
		handleChangeOperator,
		handleSpaceAggregationChange,
	} = useQueryOperations({
		index,
		query,
		entityVersion: version,
	});

	// this function is only relevant for metrics and now operators are part of aggregations
	const queryAggregation = useMemo(
		() => query.aggregations?.[0] as MetricAggregation,
		[query.aggregations],
	);

	const isHistogram = useMemo(
		() => query.aggregateAttribute?.type === ATTRIBUTE_TYPES.HISTOGRAM,
		[query.aggregateAttribute?.type],
	);

	useEffect(() => {
		setAggregationOptions([
			{
				func: queryAggregation.spaceAggregation || 'count',
				arg: queryAggregation.metricName || '',
			},
		]);
	}, [
		queryAggregation.spaceAggregation,
		queryAggregation.metricName,
		setAggregationOptions,
		query,
	]);

	const handleChangeGroupByKeys = useCallback(
		(value: IBuilderQuery['groupBy']) => {
			handleChangeQueryData('groupBy', value);
		},
		[handleChangeQueryData],
	);

	const handleChangeAggregateEvery = useCallback(
		(value: string) => {
			handleChangeQueryData('stepInterval', Number(value));
		},
		[handleChangeQueryData],
	);

	const showAggregationInterval = useMemo(() => {
		// eslint-disable-next-line sonarjs/prefer-single-boolean-return
		if (panelType === PANEL_TYPES.VALUE) {
			return false;
		}

		return true;
	}, [panelType]);

	const disableOperatorSelector =
		!queryAggregation.metricName || queryAggregation.metricName === '';

	return (
		<div
			className={cx('metrics-aggregate-section', {
				'is-histogram': isHistogram,
			})}
		>
			{!isHistogram && (
				<div className="non-histogram-container">
					<div className="metrics-time-aggregation-section">
						<div className="metrics-aggregation-section-content">
							<div className="metrics-aggregation-section-content-item">
								<div className="metrics-aggregation-section-content-item-label main-label">
									AGGREGATE BY TIME{' '}
									<Tooltip title="AGGREGATE BY TIME">
										<Info size={12} />
									</Tooltip>
								</div>
								<div className="metrics-aggregation-section-content-item-value">
									<OperatorsSelect
										value={queryAggregation.timeAggregation || ''}
										onChange={handleChangeOperator}
										operators={operators}
										className="metrics-operators-select"
									/>
								</div>
							</div>

							{showAggregationInterval && (
								<div className="metrics-aggregation-section-content-item">
									<div className="metrics-aggregation-section-content-item-label">
										every
									</div>

									<div className="metrics-aggregation-section-content-item-value">
										<InputWithLabel
											onChange={handleChangeAggregateEvery}
											label="Seconds"
											placeholder="Auto"
											labelAfter
											initialValue={query?.stepInterval ?? undefined}
										/>
									</div>
								</div>
							)}
						</div>
					</div>
					<div className="metrics-space-aggregation-section">
						<div className="metrics-aggregation-section-content">
							<div className="metrics-aggregation-section-content-item">
								<div className="metrics-aggregation-section-content-item-label main-label">
									AGGREGATE LABELS
									<Tooltip title="AGGREGATE LABELS">
										<Info size={12} />
									</Tooltip>
								</div>
								<div className="metrics-aggregation-section-content-item-value">
									<SpaceAggregationOptions
										panelType={panelType}
										key={`${panelType}${queryAggregation.spaceAggregation}${queryAggregation.timeAggregation}`}
										aggregatorAttributeType={
											query?.aggregateAttribute?.type as ATTRIBUTE_TYPES
										}
										selectedValue={queryAggregation.spaceAggregation || ''}
										disabled={disableOperatorSelector}
										onSelect={handleSpaceAggregationChange}
										operators={spaceAggregationOptions}
										qbVersion="v3"
									/>
								</div>
							</div>

							<div className="metrics-aggregation-section-content-item">
								<div className="metrics-aggregation-section-content-item-label">by</div>

								<div className="metrics-aggregation-section-content-item-value group-by-filter-container">
									<GroupByFilter
										disabled={!queryAggregation.metricName}
										query={query}
										onChange={handleChangeGroupByKeys}
									/>
								</div>
							</div>
						</div>
					</div>
				</div>
			)}

			{isHistogram && (
				<div className="metrics-space-aggregation-section">
					<div className="metrics-aggregation-section-content">
						<div className="metrics-aggregation-section-content-item">
							<div className="metrics-aggregation-section-content-item-value">
								<SpaceAggregationOptions
									panelType={panelType}
									key={`${panelType}${queryAggregation.spaceAggregation}${queryAggregation.timeAggregation}`}
									aggregatorAttributeType={
										query?.aggregateAttribute?.type as ATTRIBUTE_TYPES
									}
									selectedValue={queryAggregation.spaceAggregation || ''}
									disabled={disableOperatorSelector}
									onSelect={handleSpaceAggregationChange}
									operators={spaceAggregationOptions}
									qbVersion="v3"
								/>
							</div>
						</div>

						<div className="metrics-aggregation-section-content-item">
							<div className="metrics-aggregation-section-content-item-label">by</div>

							<div className="metrics-aggregation-section-content-item-value group-by-filter-container">
								<GroupByFilter
									disabled={!queryAggregation.metricName}
									query={query}
									onChange={handleChangeGroupByKeys}
								/>
							</div>
						</div>
						<div className="metrics-aggregation-section-content-item">
							<div className="metrics-aggregation-section-content-item-label">
								every
							</div>

							<div className="metrics-aggregation-section-content-item-value">
								<InputWithLabel
									onChange={handleChangeAggregateEvery}
									label="Seconds"
									placeholder="Auto"
									labelAfter
									initialValue={query?.stepInterval ?? undefined}
									className="histogram-every-input"
								/>
							</div>
						</div>
					</div>
				</div>
			)}
		</div>
	);
});

export default MetricsAggregateSection;
