package database

import "strings"

const (
	cteScanMaxBytes = 1 << 20
	cteScanMaxDepth = 256
)

// cteMainDMLWithoutReturning 只证明窄范围 WITH 主写语句没有顶层裸 RETURNING。
// 无法识别时保留旧 rows 路径；不是 SQL 有效性、只读性或权限证明。
// 主语句计数也不代表 PostgreSQL CTE 内部其他写入的总计数。
func cteMainDMLWithoutReturning(driver Driver, statement string) bool {
	if (driver != DriverSQLite && driver != DriverPostgreSQL) || len(statement) > cteScanMaxBytes {
		return false
	}
	scanner := cteScanner{statement: statement, driver: driver}
	if !scanner.next().word("WITH") {
		return false
	}
	token := scanner.next()
	if token.word("RECURSIVE") {
		token = scanner.next()
	}
	for {
		if !token.identifier() {
			return false
		}
		token = scanner.next()
		if token.kind == '(' {
			// 列名列表必须非空，且只能由标识符和逗号交替组成。
			for {
				if !scanner.next().identifier() {
					return false
				}
				token = scanner.next()
				if token.kind != ',' {
					break
				}
			}
			if token.kind != ')' {
				return false
			}
			token = scanner.next()
		}
		if !token.word("AS") {
			return false
		}
		token = scanner.next()
		if token.word("NOT") {
			if !scanner.next().word("MATERIALIZED") {
				return false
			}
			token = scanner.next()
		} else if token.word("MATERIALIZED") {
			token = scanner.next()
		}
		if token.kind != '(' || !scanner.cteBody() {
			return false
		}
		token = scanner.next()
		if token.kind != ',' {
			break
		}
		token = scanner.next()
	}
	// SQLite 的 REPLACE 是 INSERT OR REPLACE 别名；其他方言不扩展识别。
	if !token.word("INSERT") && !token.word("UPDATE") && !token.word("DELETE") && !(driver == DriverSQLite && token.word("REPLACE")) {
		return false
	}
	depth, hasBody, ended := 0, false, false
	for {
		token = scanner.next()
		if token.kind == cteTokenInvalid {
			return false
		}
		if token.kind == cteTokenEOF {
			return depth == 0 && hasBody
		}
		if ended || (depth == 0 && token.word("RETURNING")) {
			return false
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
		case ';':
			if depth != 0 {
				return false
			}
			ended = true
		default:
			hasBody = true
		}
	}
}

const (
	cteTokenInvalid byte = iota
	cteTokenEOF
	cteTokenWord
	cteTokenIdentifier
	cteTokenLiteral
	cteTokenNumber
)

type cteToken struct {
	kind byte
	text string
}

func (t cteToken) word(want string) bool {
	return t.kind == cteTokenWord && strings.EqualFold(t.text, want)
}

func (t cteToken) identifier() bool {
	return t.kind == cteTokenWord || t.kind == cteTokenIdentifier
}

// 游标只向前移动，不递归、不保留 token 数组，也不复制或大写整个 SQL。
// 只有完整扫描成功才可改变执行路径；未知词法和资源边界都返回 false。
type cteScanner struct {
	statement string
	driver    Driver
	position  int
}

func (s *cteScanner) cteBody() bool {
	depth, hasBody := 1, false
	for depth > 0 {
		token := s.next()
		switch token.kind {
		case cteTokenInvalid, cteTokenEOF, ';':
			return false
		case '(':
			depth++
			if depth > cteScanMaxDepth {
				return false
			}
		case ')':
			depth--
		default:
			hasBody = true
		}
	}
	return hasBody
}

func (s *cteScanner) next() cteToken {
	for s.position < len(s.statement) {
		c := s.statement[s.position]
		if size := querySpacePrefix(s.driver, s.statement[s.position:]); size > 0 {
			s.position += size
			continue
		}
		if strings.HasPrefix(s.statement[s.position:], "--") {
			s.position += 2
			for s.position < len(s.statement) {
				c = s.statement[s.position]
				if c == 0 {
					return cteToken{}
				}
				if c == '\n' || (s.driver == DriverPostgreSQL && c == '\r') {
					break
				}
				s.position++
			}
			continue
		}
		if strings.HasPrefix(s.statement[s.position:], "/*") {
			if !s.blockComment() {
				return cteToken{}
			}
			continue
		}
		break
	}
	if s.position == len(s.statement) {
		return cteToken{kind: cteTokenEOF}
	}
	start := s.position
	c := s.statement[s.position]
	if cteWordStart(c) {
		s.position++
		for s.position < len(s.statement) && (cteWordStart(s.statement[s.position]) || cteDigit(s.statement[s.position])) {
			s.position++
		}
		// SQLite 的裸标识符允许非 ASCII 字节；不能在标识符内部把 BOM 拆为空白。
		// 这些标识符仍超出本扫描器范围，保留未知词法回退。
		if s.driver == DriverSQLite && s.position < len(s.statement) && s.statement[s.position] >= 0x80 {
			return cteToken{}
		}
		word := s.statement[start:s.position]
		if s.driver == DriverPostgreSQL && len(word) == 1 {
			tail := s.statement[s.position:]
			switch word[0] {
			case 'e', 'E', 'b', 'B', 'x', 'X', 'n', 'N':
				if strings.HasPrefix(tail, "'") {
					return cteToken{}
				}
			case 'u', 'U':
				if strings.HasPrefix(tail, "&'") || strings.HasPrefix(tail, "&\"") {
					return cteToken{}
				}
			}
		}
		return cteToken{kind: cteTokenWord, text: word}
	}
	if cteDigit(c) {
		return s.number()
	}
	switch c {
	case '\'', '"':
		return s.quoted(c)
	case '`', '[':
		if s.driver == DriverSQLite {
			return s.quoted(c)
		}
	case ':':
		if s.driver == DriverPostgreSQL && strings.HasPrefix(s.statement[s.position:], "::") {
			s.position += 2
			return cteToken{kind: ':'}
		}
	case '(', ')', ',', '.', ';', '+', '-', '*', '/', '%', '=', '<', '>', '!', '|', '&', '^', '~':
		s.position++
		return cteToken{kind: c}
	}
	// 包括 dollar quoting、参数、未引用非 ASCII 和未知标点。
	return cteToken{}
}

func (s *cteScanner) blockComment() bool {
	depth := 0
	for s.position < len(s.statement) {
		tail := s.statement[s.position:]
		switch {
		case strings.HasPrefix(tail, "/*") && (depth == 0 || s.driver == DriverPostgreSQL):
			if strings.HasPrefix(tail, "/*!") || strings.HasPrefix(tail, "/*M!") || strings.HasPrefix(tail, "/*m!") {
				return false
			}
			depth++
			if depth > cteScanMaxDepth {
				return false
			}
			s.position += 2
		case strings.HasPrefix(tail, "*/"):
			depth--
			s.position += 2
			if depth == 0 {
				return true
			}
		default:
			if s.statement[s.position] == 0 {
				return false
			}
			s.position++
		}
	}
	return false
}

func (s *cteScanner) quoted(open byte) cteToken {
	kind, close := byte(cteTokenIdentifier), open
	if open == '\'' {
		kind = cteTokenLiteral
	} else if open == '[' {
		close = ']'
	}
	s.position++
	for s.position < len(s.statement) {
		c := s.statement[s.position]
		if c == 0 || (s.driver == DriverPostgreSQL && open == '\'' && c == '\\') {
			return cteToken{}
		}
		s.position++
		if c == close {
			// SQLite 方括号只到第一个 ]；其余引用支持双写终止符。
			if open != '[' && s.position < len(s.statement) && s.statement[s.position] == close {
				s.position++
				continue
			}
			return cteToken{kind: kind}
		}
	}
	return cteToken{}
}

func (s *cteScanner) number() cteToken {
	for s.position < len(s.statement) && cteDigit(s.statement[s.position]) {
		s.position++
	}
	if s.position < len(s.statement) && s.statement[s.position] == '.' {
		s.position++
		for s.position < len(s.statement) && cteDigit(s.statement[s.position]) {
			s.position++
		}
	}
	if s.position < len(s.statement) && (s.statement[s.position] == 'e' || s.statement[s.position] == 'E') {
		s.position++
		if s.position < len(s.statement) && (s.statement[s.position] == '+' || s.statement[s.position] == '-') {
			s.position++
		}
		start := s.position
		for s.position < len(s.statement) && cteDigit(s.statement[s.position]) {
			s.position++
		}
		if s.position == start {
			return cteToken{}
		}
	}
	if s.position < len(s.statement) && (cteWordStart(s.statement[s.position]) || s.statement[s.position] >= 0x80) {
		return cteToken{}
	}
	return cteToken{kind: cteTokenNumber}
}

func cteWordStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func cteDigit(c byte) bool { return c >= '0' && c <= '9' }
