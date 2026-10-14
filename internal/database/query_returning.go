package database

// sqliteDMLReturning 只识别 SQLite 直接 INSERT/UPDATE/DELETE 的返回子句。
// SQLite 的 RETURNING terminal 不属于标识符或 fallback，区别于 PostgreSQL
// 允许 AS returning、t.returning 的语法。依据 SQLite 3.53.3 src/parse.y。
// 完整扫描成功才选择 rows；未知词法、批次或超限保留旧 exec，不拒绝 SQL。
func sqliteDMLReturning(driver Driver, statement string) bool {
	if driver != DriverSQLite || len(statement) > cteScanMaxBytes {
		return false
	}
	scanner := cteScanner{driver: driver, statement: statement}
	first := scanner.next()
	if !first.word("INSERT") && !first.word("UPDATE") && !first.word("DELETE") {
		return false
	}
	depth := 0
	found, tail, ended := false, false, false
	for {
		token := scanner.next()
		if token.kind == cteTokenInvalid {
			return false
		}
		if token.kind == cteTokenEOF {
			return depth == 0 && found && tail
		}
		if ended {
			return false
		}
		if token.kind == ';' {
			if depth != 0 {
				return false
			}
			ended = true
			continue
		}
		if depth == 0 && token.word("RETURNING") {
			if found {
				return false
			}
			found = true
			continue
		}
		if found {
			tail = true
		}
		switch token.kind {
		case '(':
			depth++
			if depth > cteScanMaxDepth {
				return false
			}
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
}
