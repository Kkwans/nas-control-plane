package database

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"
)

const maxSafeRowsAffected int64 = 9007199254740991

// ValidateQueryResultMode 在连接数据库之前拒绝无效模式。零值兼容旧请求。
func ValidateQueryResultMode(mode QueryResultMode) error {
	switch mode {
	case "", QueryResultModeAuto, QueryResultModeRows, QueryResultModeExec:
		return nil
	default:
		return newDatabaseError(CodeSQLInvalid, Source{}, "query", errors.New("invalid result mode"))
	}
}

type queryRows interface {
	Columns() ([]string, error)
	ColumnTypes() ([]*sql.ColumnType, error)
	Next() bool
	Scan(...any) error
	Close() error
	Err() error
}

type queryRunner interface {
	exec(context.Context, string) (sql.Result, error)
	query(context.Context, string) (queryRows, error)
}

type databaseQueryRunner struct{ db *sql.DB }

func (r databaseQueryRunner) exec(ctx context.Context, statement string) (sql.Result, error) {
	return r.db.ExecContext(ctx, statement)
}

func (r databaseQueryRunner) query(ctx context.Context, statement string) (queryRows, error) {
	return r.db.QueryContext(ctx, statement)
}

func executeQuery(ctx context.Context, runner queryRunner, driver Driver, request QueryRequest) (QueryResult, error) {
	if err := ValidateQueryResultMode(request.ResultMode); err != nil {
		return QueryResult{}, err
	}
	started := time.Now()
	useRows := request.ResultMode == QueryResultModeRows ||
		(request.ResultMode != QueryResultModeExec && returnsRows(driver, request.SQL))
	if !useRows {
		// 只选择一次执行路径，取计数失败也绝不重新执行 SQL。
		executed, err := runner.exec(ctx, request.SQL)
		if err != nil {
			return QueryResult{}, err
		}
		result := QueryResult{Columns: []string{}, Rows: [][]any{}}
		if executed != nil {
			if count, countErr := executed.RowsAffected(); countErr == nil && count >= 0 && count <= maxSafeRowsAffected {
				result.RowsAffected, result.RowsAffectedKnown = count, true
			}
		}
		result.DurationMs = time.Since(started).Milliseconds()
		return result, nil
	}
	rows, err := runner.query(ctx, request.SQL)
	if err != nil {
		return QueryResult{}, err
	}
	result, err := readQueryRows(ctx, rows)
	if err != nil {
		return QueryResult{}, err
	}
	result.DurationMs = time.Since(started).Milliseconds()
	return result, nil
}

func readQueryRows(ctx context.Context, rows queryRows) (QueryResult, error) {
	finish := func(primary error) error { return finishResultRows(ctx, rows, primary) }
	columns, err := rows.Columns()
	if err != nil {
		return QueryResult{}, finish(err)
	}
	if columns == nil {
		columns = []string{}
	}
	result := QueryResult{Columns: columns, Rows: [][]any{}}
	// 先预留最坏元数据；计时和截断原因不会在最后把响应挤出预算。
	reservation := result
	reservation.TruncationReason = "byte_limit"
	reservation.DurationMs = math.MaxInt64
	budget, err := newResultBudget(reservation)
	if err != nil {
		return QueryResult{}, finish(err)
	}
	result.Columns = ownResultColumns(columns)
	columnTypes, _ := rows.ColumnTypes()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return QueryResult{}, finish(err)
		}
		if len(result.Rows) >= maxQueryRows {
			result.Truncated = true
			result.TruncationReason = "row_limit"
			break
		}
		rowBudget := *budget
		scanErr := rowBudget.take(resultNodeCost)
		var values []any
		if scanErr == nil {
			values, scanErr = scanBudgetValues(rows, len(columns), columnTypes, &rowBudget)
		}
		if ErrorCodeOf(scanErr) == CodeResultTooLarge {
			result.Truncated, result.TruncationReason = true, "byte_limit"
			break
		}
		if scanErr != nil {
			return QueryResult{}, finish(scanErr)
		}
		*budget = rowBudget
		result.Rows = append(result.Rows, values)
	}
	if err := finish(nil); err != nil {
		return QueryResult{}, err
	}
	// Query 路径没有通用影响行数；返回行数不能作为写入计数。
	return result, nil
}

// Close 可能消费剩余协议并返回后续错误，必须在宣布成功之前检查。
func finishResultRows(ctx context.Context, rows queryRows, primary error) error {
	closeErr := rows.Close()
	iterationErr := rows.Err()
	if primary != nil {
		return primary
	}
	if iterationErr != nil {
		return iterationErr
	}
	if closeErr != nil {
		return closeErr
	}
	return ctx.Err()
}

func returnsRows(driver Driver, statement string) bool {
	switch leadingQueryKeyword(driver, statement) {
	case "WITH":
		return !cteMainDMLWithoutReturning(driver, statement)
	case "INSERT", "UPDATE", "DELETE":
		return sqliteDMLReturning(driver, statement)
	case "SELECT", "PRAGMA", "EXPLAIN", "SHOW", "DESCRIBE", "DESC", "VALUES":
		return true
	default:
		return false
	}
}

// leadingQueryKeyword 只扫描前导普通注释。它不是 SQL parser 或只读证明，
// 不判断 RETURNING/CTE 主语句，也不会改写提交给驱动的 SQL 原文。
func leadingQueryKeyword(driver Driver, statement string) string {
	for len(statement) > 0 {
		for len(statement) > 0 && sqlSpace(statement[0]) {
			statement = statement[1:]
		}
		if strings.HasPrefix(statement, "--") &&
			(driver != DriverMySQL || (len(statement) > 2 && (statement[2] <= ' ' || statement[2] == 0x7f))) {
			statement = skipQueryLineComment(driver, statement[2:])
			continue
		}
		if driver == DriverMySQL && strings.HasPrefix(statement, "#") {
			statement = skipQueryLineComment(driver, statement[1:])
			continue
		}
		if strings.HasPrefix(statement, "/*") {
			if driver == DriverMySQL && (strings.HasPrefix(statement, "/*!") || strings.HasPrefix(statement, "/*M!") || strings.HasPrefix(statement, "/*m!")) {
				return ""
			}
			depth, position := 1, 2
			for position < len(statement) && depth > 0 {
				switch {
				case strings.HasPrefix(statement[position:], "*/"):
					depth--
					position += 2
				case driver == DriverPostgreSQL && strings.HasPrefix(statement[position:], "/*"):
					depth++
					position += 2
				default:
					position++
				}
			}
			if depth != 0 {
				return ""
			}
			statement = statement[position:]
			continue
		}
		end := 0
		for end < len(statement) && queryKeywordByte(statement[end]) {
			end++
		}
		return strings.ToUpper(statement[:end])
	}
	return ""
}

func sqlSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r' || value == '\f' || value == '\v'
}

func queryKeywordByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' || value == '_' || value == '$' || value >= 0x80
}

func skipQueryLineComment(driver Driver, statement string) string {
	// PostgreSQL 的 scanner 将 CR/LF 都视为行注释终止符；其他驱动保持现有 LF 行为。
	terminators := "\n"
	if driver == DriverPostgreSQL {
		terminators = "\r\n"
	}
	if end := strings.IndexAny(statement, terminators); end >= 0 {
		return statement[end:]
	}
	return ""
}
