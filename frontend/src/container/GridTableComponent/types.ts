import { TableProps } from 'antd';
import { LogsExplorerTableProps } from 'container/LogsExplorerTable/LogsExplorerTable.interfaces';
import {
	ThresholdOperators,
	ThresholdProps,
} from 'container/NewWidget/RightContainer/Threshold/types';
import { QueryTableProps } from 'container/QueryTable/QueryTable.intefaces';
import { RowData } from 'lib/query/createTableColumnsFromQuery';
import { ColumnUnit } from 'types/api/dashboard/getAll';
import { Query } from 'types/api/queryBuilder/queryBuilderData';

export type GridTableComponentProps = {
	query: Query;
	thresholds?: ThresholdProps[];
	columnUnits?: ColumnUnit;
	tableProcessedDataRef?: React.MutableRefObject<RowData[]>;
	sticky?: TableProps<RowData>['sticky'];
	searchTerm?: string;
	openTracesButton?: boolean;
	onOpenTraceBtnClick?: (record: RowData) => void;
	customOnRowClick?: (record: RowData) => void;
	widgetId?: string;
	renderColumnCell?: QueryTableProps['renderColumnCell'];
	customColTitles?: Record<string, string>;
} & Pick<LogsExplorerTableProps, 'data'> &
	Omit<TableProps<RowData>, 'columns' | 'dataSource'>;

export type RequiredThresholdProps = Omit<
	ThresholdProps,
	'thresholdTableOptions' | 'thresholdOperator' | 'thresholdValue'
> & {
	thresholdTableOptions: string;
	thresholdOperator: ThresholdOperators;
	thresholdValue: number;
};
