package database

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
)

// 目录复用响应保留预算；驱动可能已经分配单个值，不是进程堆上限。
func chargeCatalogValue(budget *resultBudget, value any) error {
	return budget.value(reflect.ValueOf(value), 0)
}

// nil 默认值在协议中省略；其他值保持 normalizeValue 的既有语义。
type catalogDefaultCell struct {
	budget *resultBudget
	value  any
}

func (cell *catalogDefaultCell) Scan(value any) error {
	if value == nil {
		return nil
	}
	scanner := budgetCell{budget: cell.budget}
	if err := scanner.Scan(value); err != nil {
		return err
	}
	cell.value = scanner.value
	return nil
}

func retainCatalogColumn(ctx context.Context, budget *resultBudget, column *Column, first, sqlite bool) error {
	if budget == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if first {
		if err := budget.text("columns"); err != nil {
			return err
		}
		if err := budget.take(resultNodeCost); err != nil {
			return err
		}
	}
	shape := *column
	// 默认值已在 Scanner 内计费，只补字段键；不要再次计费或复制。
	shape.Default = nil
	if sqlite {
		// SQLite 在 index_list 后才能判断 rowid；预留最长写入模式。
		shape.WriteMode = "server-generated"
	}
	if err := chargeCatalogValue(budget, shape); err != nil {
		return err
	}
	if column.Default != nil {
		if err := budget.text("default"); err != nil {
			return err
		}
	}
	column.Name = strings.Clone(column.Name)
	column.DataType = strings.Clone(column.DataType)
	return nil
}

func cloneCatalogTableText(table *Table) {
	table.Schema = strings.Clone(table.Schema)
	table.Name = strings.Clone(table.Name)
	table.Type = strings.Clone(table.Type)
	table.Definition = strings.Clone(table.Definition)
	table.CreatedAt = strings.Clone(table.CreatedAt)
}

func enrichCatalogTable(ctx context.Context, db *sql.DB, driver Driver, table *Table, budget *resultBudget) error {
	pending := *table
	// 只核算固定表字段的变化，不能反复遍历不断增长的列集合。
	before := *table
	before.Columns = nil
	trial := *budget
	checkpoint := func() error {
		after := pending
		after.Columns = nil
		previous, err := newResultBudget(before)
		if err != nil {
			return err
		}
		next := trial
		next.remaining += MaxResultBytes - previous.remaining
		if err := chargeCatalogValue(&next, after); err != nil {
			return err
		}
		before, trial = after, next
		return nil
	}
	if err := enrichTable(ctx, db, driver, &pending, checkpoint); err != nil {
		return err
	}
	pending.Definition = strings.Clone(pending.Definition)
	pending.CreatedAt = strings.Clone(pending.CreatedAt)
	*budget, *table = trial, pending
	return nil
}
