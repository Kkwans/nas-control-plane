package database

import "context"

func readPagedRows(ctx context.Context, rows queryRows, result RowsResult, budget *resultBudget) (RowsResult, error) {
	finish := func(primary error) error { return finishResultRows(ctx, rows, primary) }
	names, err := rows.Columns()
	if err != nil {
		return RowsResult{}, finish(err)
	}
	// 即使表元数据与驱动列名不同，也不能按无界列数分配扫描目标。
	if err := ValidateResultBudget(names); err != nil {
		return RowsResult{}, finish(err)
	}
	names = ownResultColumns(names)
	columnTypes, _ := rows.ColumnTypes()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return RowsResult{}, finish(err)
		}
		// limit+1 行只用于探测，不 Scan、不复制、不规范化大单元。
		if len(result.Rows) == result.Limit {
			result.HasMore = result.Ordering.Stable
			break
		}
		rowBudget := *budget
		if err := rowBudget.take(resultNodeCost); err != nil {
			return RowsResult{}, finish(err)
		}
		for _, name := range names {
			if err := rowBudget.text(name); err != nil {
				return RowsResult{}, finish(err)
			}
		}
		values, scanErr := scanBudgetValues(rows, len(names), columnTypes, &rowBudget)
		if scanErr != nil {
			// 固定 offset 分页不能返回短页，否则下一页会漏行。
			return RowsResult{}, finish(scanErr)
		}
		row := make(Row, len(names))
		for index, name := range names {
			row[name] = values[index]
		}
		result.Rows = append(result.Rows, row)
		*budget = rowBudget
	}
	if err := finish(nil); err != nil {
		return RowsResult{}, err
	}
	return result, nil
}
