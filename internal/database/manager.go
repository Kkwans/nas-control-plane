package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

const (
	defaultPageSize = 50
	maxPageSize     = 200
	maxQueryRows    = 500
	maxScanFiles    = 2500
	localDockerHost = "unix:///var/run/docker.sock"
)

type sqliteCandidate struct {
	root    string
	project string
	module  string
}

type Manager struct {
	now        func() time.Time
	registry   sourceRegistry
	discoverFn func(context.Context) (Discovery, error)
}

const discoveryTTL = 10 * time.Second

type sourceRegistry struct {
	mu         sync.Mutex
	snapshot   Discovery
	expiresAt  time.Time
	refreshing bool
	wait       chan struct{}
}

func NewManager() *Manager {
	return &Manager{now: time.Now}
}

func (m *Manager) TestConnection(ctx context.Context, request Connection) (ConnectionDiagnostic, error) {
	started := time.Now()
	source, db, err := m.connect(ctx, request)
	if err != nil {
		return diagnosticForError(Source{ID: request.SourceID}, "test_connection", started, err), err
	}
	defer db.Close()
	return ConnectionDiagnostic{
		Connected:  true,
		Code:       "connected",
		Driver:     source.Driver,
		Endpoint:   sourceEndpoint(source, request.Credentials.Database),
		Database:   effectiveDatabase(source, request.Credentials.Database),
		Operation:  "test_connection",
		DurationMs: time.Since(started).Milliseconds(),
	}, nil
}

func (m *Manager) Discover(ctx context.Context) (Discovery, error) {
	return m.discover(ctx, false)
}

// DiscoverWithOptions serves the explicit UI "重新发现" action. Normal
// operations use the short-lived snapshot returned by Discover instead of
// rescanning Docker and the filesystem for every request.
func (m *Manager) DiscoverWithOptions(ctx context.Context, force bool) (Discovery, error) {
	return m.discover(ctx, force)
}

func (m *Manager) discover(ctx context.Context, force bool) (Discovery, error) {
	now := m.now().UTC()
	m.registry.mu.Lock()
	if !force && !m.registry.snapshot.CollectedAt.IsZero() && now.Before(m.registry.expiresAt) {
		result := m.registry.snapshot
		m.registry.mu.Unlock()
		return result, nil
	}
	if m.registry.refreshing {
		wait := m.registry.wait
		m.registry.mu.Unlock()
		select {
		case <-wait:
			m.registry.mu.Lock()
			result, expiresAt := m.registry.snapshot, m.registry.expiresAt
			m.registry.mu.Unlock()
			if !result.CollectedAt.IsZero() && m.now().UTC().Before(expiresAt) {
				return result, nil
			}
			return Discovery{}, errors.New("database discovery refresh failed")
		case <-ctx.Done():
			return Discovery{}, ctx.Err()
		}
	}
	m.registry.refreshing = true
	m.registry.wait = make(chan struct{})
	wait := m.registry.wait
	m.registry.mu.Unlock()

	discoverFn := m.discoverFn
	if discoverFn == nil {
		discoverFn = m.discoverFresh
	}
	result, err := discoverFn(ctx)
	m.registry.mu.Lock()
	m.registry.refreshing = false
	if err == nil {
		m.registry.snapshot = result
		m.registry.expiresAt = m.now().UTC().Add(discoveryTTL)
	}
	close(wait)
	m.registry.mu.Unlock()
	return result, err
}

func (m *Manager) discoverFresh(ctx context.Context) (Discovery, error) {
	sources := make([]Source, 0, 12)
	sourceIDs := make(map[string]struct{})
	addSource := func(source Source) {
		if _, exists := sourceIDs[source.ID]; exists {
			return
		}
		sourceIDs[source.ID] = struct{}{}
		sources = append(sources, source)
	}
	addSQLite := func(path, name, category, project, module string, tags ...string) {
		if !isSQLiteFile(ctx, path) {
			return
		}
		addSource(Source{
			ID: sourceID(DriverSQLite, path), Name: name, Driver: DriverSQLite,
			Category: category, Project: project, Module: module, Location: path,
			Path: path, Status: "available", Reachability: "host", Evidence: "host-path", Tags: tags,
		})
	}

	addSQLite(
		"/volume1/@appstore/com.ugreen.docker/db/docker_info_log.db",
		"绿联 Docker 管理数据库", "system", "绿联 NAS", "Docker 项目、仓库与设置",
		"系统数据库", "Docker 项目",
	)
	addSQLite(
		"/volume2/Project/nas-control-plane/data/ncp.sqlite",
		"NAS 管理面板数据库", "project", "nas-control-plane", "用户与会话",
		"项目数据库",
	)
	addSQLite(
		"/volume2/Project/nas-file-browser/database/filebrowser.db",
		"NAS 文件浏览器数据库", "project", "nas-file-browser", "文件浏览器业务数据",
		"项目数据库",
	)

	known := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		known[source.Path] = struct{}{}
	}
	for _, root := range []string{"/volume2/Project", "/volume2/DockerProject"} {
		for _, path := range discoverSQLiteFiles(ctx, root) {
			if _, exists := known[path]; exists {
				continue
			}
			project, module := associationForPath(path)
			addSQLite(path, filepath.Base(path), "project", project, module, "自动发现")
			known[path] = struct{}{}
		}
	}

	dockerSources, sqliteCandidates := discoverDockerDatabases(ctx)
	for _, source := range dockerSources {
		addSource(source)
	}
	for _, candidate := range sqliteCandidates {
		info, err := os.Stat(candidate.root)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			addSQLite(candidate.root, candidate.project+" · "+filepath.Base(candidate.root), "project", candidate.project, candidate.module, "容器挂载", "自动发现")
			continue
		}
		for _, path := range discoverSQLiteFiles(ctx, candidate.root) {
			addSQLite(path, candidate.project+" · "+filepath.Base(path), "project", candidate.project, candidate.module, "容器挂载", "自动发现")
		}
	}

	sort.Slice(sources, func(i, j int) bool {
		if sources[i].Category != sources[j].Category {
			return sources[i].Category < sources[j].Category
		}
		return sources[i].Name < sources[j].Name
	})
	return Discovery{CollectedAt: m.now().UTC(), Sources: sources}, nil
}

func (m *Manager) Catalog(ctx context.Context, request CatalogRequest) (Catalog, error) {
	source, db, err := m.connect(ctx, request.Connection)
	if err != nil {
		return Catalog{}, err
	}
	defer db.Close()
	tables, err := listTables(ctx, db, source, request.Credentials)
	if err != nil {
		return Catalog{}, MapError(ctx, source, "catalog", err)
	}
	for index := range tables {
		columns, columnErr := listColumns(ctx, db, source.Driver, tables[index].Schema, tables[index].Name)
		if columnErr != nil {
			return Catalog{}, MapError(ctx, source, "catalog_columns", columnErr)
		}
		tables[index].Columns = columns
		enrichTable(ctx, db, source.Driver, &tables[index])
	}
	return Catalog{Source: source, Tables: tables}, nil
}

func (m *Manager) Query(ctx context.Context, request QueryRequest) (QueryResult, error) {
	statement := strings.TrimSpace(request.SQL)
	if statement == "" {
		return QueryResult{}, newDatabaseError(CodeSQLInvalid, Source{ID: request.SourceID}, "query", errors.New("SQL statement is empty"))
	}
	source, db, err := m.connect(ctx, request.Connection)
	if err != nil {
		return QueryResult{}, err
	}
	defer db.Close()
	started := time.Now()
	if !returnsRows(statement) {
		result, execErr := db.ExecContext(ctx, statement)
		if execErr != nil {
			return QueryResult{}, MapError(ctx, source, "query", execErr)
		}
		affected, _ := result.RowsAffected()
		return QueryResult{Columns: []string{}, Rows: [][]any{}, RowsAffected: affected, DurationMs: time.Since(started).Milliseconds()}, nil
	}
	rows, err := db.QueryContext(ctx, statement)
	if err != nil {
		return QueryResult{}, MapError(ctx, source, "query", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return QueryResult{}, MapError(ctx, source, "query", err)
	}
	result := QueryResult{Columns: columns, Rows: make([][]any, 0), DurationMs: 0}
	columnTypes, _ := rows.ColumnTypes()
	for rows.Next() {
		if len(result.Rows) >= maxQueryRows {
			result.Truncated = true
			break
		}
		values, scanErr := scanValues(rows, len(columns), columnTypes)
		if scanErr != nil {
			return QueryResult{}, MapError(ctx, source, "query", scanErr)
		}
		result.Rows = append(result.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return QueryResult{}, MapError(ctx, source, "query", err)
	}
	result.DurationMs = time.Since(started).Milliseconds()
	return result, nil
}

func (m *Manager) Rows(ctx context.Context, request RowsRequest) (RowsResult, error) {
	source, db, err := m.connect(ctx, request.Connection)
	if err != nil {
		return RowsResult{}, err
	}
	defer db.Close()
	table, err := loadTable(ctx, db, source.Driver, request.Schema, request.Table)
	if err != nil {
		return RowsResult{}, MapError(ctx, source, "rows", err)
	}
	limit := request.Limit
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}
	if request.Offset < 0 {
		request.Offset = 0
	}
	order, ordering := rowsOrder(source.Driver, table, request)
	if ordering.Columns == nil {
		return RowsResult{}, newDatabaseError(CodeSQLInvalid, source, "rows", errors.New("sort column does not exist"))
	}
	query := "SELECT * FROM " + qualified(source.Driver, table.Schema, table.Name) + order +
		" LIMIT " + strconv.Itoa(limit+1) + " OFFSET " + strconv.Itoa(request.Offset)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return RowsResult{}, MapError(ctx, source, "rows", err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return RowsResult{}, MapError(ctx, source, "rows", err)
	}
	result := RowsResult{Table: table, Rows: make([]Row, 0, limit), Limit: limit, Offset: request.Offset, Ordering: ordering}
	columnTypes, _ := rows.ColumnTypes()
	for rows.Next() {
		values, scanErr := scanValues(rows, len(names), columnTypes)
		if scanErr != nil {
			return RowsResult{}, MapError(ctx, source, "rows", scanErr)
		}
		if len(result.Rows) == limit {
			result.HasMore = ordering.Stable
			break
		}
		row := make(Row, len(names))
		for index, name := range names {
			row[name] = values[index]
		}
		result.Rows = append(result.Rows, row)
	}
	return result, MapError(ctx, source, "rows", rows.Err())
}

func (m *Manager) Insert(ctx context.Context, request InsertRequest) (MutationResult, error) {
	source, db, err := m.connect(ctx, request.Connection)
	if err != nil {
		return MutationResult{}, err
	}
	defer db.Close()
	table, err := loadTable(ctx, db, source.Driver, request.Schema, request.Table)
	if err != nil {
		return MutationResult{}, MapError(ctx, source, "insert", err)
	}
	columns, args, err := mutationValues(table, request.Values)
	if err != nil {
		return MutationResult{}, MapError(ctx, source, "insert", err)
	}
	if len(columns) == 0 {
		statement := "INSERT INTO " + qualified(source.Driver, table.Schema, table.Name)
		if source.Driver == DriverMySQL {
			statement += " () VALUES ()"
		} else {
			statement += " DEFAULT VALUES"
		}
		result, execErr := execMutation(ctx, db, statement, nil)
		return result, MapError(ctx, source, "insert", execErr)
	}
	placeholders := make([]string, len(columns))
	for index := range placeholders {
		placeholders[index] = placeholder(source.Driver, index+1)
		columns[index] = quote(source.Driver, columns[index])
	}
	statement := "INSERT INTO " + qualified(source.Driver, table.Schema, table.Name) +
		" (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	result, err := execMutation(ctx, db, statement, args)
	return result, MapError(ctx, source, "insert", err)
}

func (m *Manager) Update(ctx context.Context, request UpdateRequest) (MutationResult, error) {
	source, db, err := m.connect(ctx, request.Connection)
	if err != nil {
		return MutationResult{}, err
	}
	defer db.Close()
	table, err := loadTable(ctx, db, source.Driver, request.Schema, request.Table)
	if err != nil {
		return MutationResult{}, MapError(ctx, source, "update", err)
	}
	columns, args, err := mutationValues(table, request.Values)
	if err != nil {
		return MutationResult{}, MapError(ctx, source, "update", err)
	}
	if len(columns) == 0 {
		return MutationResult{}, MapError(ctx, source, "update", errors.New("没有需要写入的字段"))
	}
	setParts := make([]string, len(columns))
	for index, column := range columns {
		setParts[index] = quote(source.Driver, column) + " = " + placeholder(source.Driver, index+1)
	}
	where, keyArgs, err := primaryKeyWhere(source.Driver, table, request.Keys, len(args))
	if err != nil {
		return MutationResult{}, MapError(ctx, source, "update", err)
	}
	args = append(args, keyArgs...)
	statement := "UPDATE " + qualified(source.Driver, table.Schema, table.Name) +
		" SET " + strings.Join(setParts, ", ") + " WHERE " + where
	result, err := execMutation(ctx, db, statement, args)
	return result, MapError(ctx, source, "update", err)
}

func (m *Manager) Delete(ctx context.Context, request DeleteRequest) (MutationResult, error) {
	source, db, err := m.connect(ctx, request.Connection)
	if err != nil {
		return MutationResult{}, err
	}
	defer db.Close()
	table, err := loadTable(ctx, db, source.Driver, request.Schema, request.Table)
	if err != nil {
		return MutationResult{}, MapError(ctx, source, "delete", err)
	}
	where, args, err := primaryKeyWhere(source.Driver, table, request.Keys, 0)
	if err != nil {
		return MutationResult{}, MapError(ctx, source, "delete", err)
	}
	statement := "DELETE FROM " + qualified(source.Driver, table.Schema, table.Name) + " WHERE " + where
	result, err := execMutation(ctx, db, statement, args)
	return result, MapError(ctx, source, "delete", err)
}

func (m *Manager) connect(ctx context.Context, connection Connection) (Source, *sql.DB, error) {
	discovery, err := m.Discover(ctx)
	if err != nil {
		return Source{}, nil, MapError(ctx, Source{}, "discover", err)
	}
	var source Source
	for _, candidate := range discovery.Sources {
		if candidate.ID == connection.SourceID {
			source = candidate
			break
		}
	}
	if source.ID == "" {
		return Source{}, nil, newDatabaseError(CodeDatabaseNotFound, Source{ID: connection.SourceID}, "connect", errors.New("database source is unavailable"))
	}
	driver, dsn, err := connectionString(source, connection.Credentials)
	if err != nil {
		return Source{}, nil, MapError(ctx, source, "connect", err)
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return Source{}, nil, MapError(ctx, source, "connect", err)
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return Source{}, nil, MapError(ctx, source, "connect", err)
	}
	return source, db, nil
}

func connectionString(source Source, credentials Credentials) (string, string, error) {
	if source.Reachability == "container-internal" || source.Reachability == "unreachable" || source.Evidence == "none" {
		return "", "", errors.New("database source is not reachable from the host agent")
	}
	switch source.Driver {
	case DriverSQLite:
		if strings.TrimSpace(source.Path) == "" {
			return "", "", errors.New("sqlite path is required")
		}
		return "sqlite", source.Path, nil
	case DriverMySQL:
		databaseName := effectiveDatabase(source, credentials.Database)
		if credentials.Username == "" || databaseName == "" {
			return "", "", errors.New("mysql username and database are required")
		}
		host, port, _ := endpointParts(source.Location, source.Port, databaseName)
		if strings.TrimSpace(source.Host) != "" {
			host = source.Host
		}
		if strings.TrimSpace(host) == "" || port <= 0 {
			return "", "", errors.New("mysql endpoint is required")
		}
		config := mysql.Config{
			User: credentials.Username, Passwd: credentialSecret(credentials),
			Net: "tcp", Addr: net.JoinHostPort(host, strconv.Itoa(port)), DBName: databaseName,
			ParseTime: true, Params: map[string]string{"charset": "utf8mb4"},
		}
		return "mysql", config.FormatDSN(), nil
	case DriverPostgreSQL:
		if credentials.Username == "" {
			return "", "", errors.New("postgresql username is required")
		}
		databaseName := effectiveDatabase(source, credentials.Database)
		if databaseName == "" {
			return "", "", errors.New("postgresql database is required")
		}
		host, port, _ := endpointParts(source.Location, source.Port, databaseName)
		if strings.TrimSpace(source.Host) != "" {
			host = source.Host
		}
		if strings.TrimSpace(host) == "" || port <= 0 {
			return "", "", errors.New("postgresql endpoint is required")
		}
		values := url.Values{"sslmode": []string{"disable"}}
		target := &url.URL{
			Scheme: "postgres", User: url.UserPassword(credentials.Username, credentialSecret(credentials)),
			Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: databaseName,
			RawQuery: values.Encode(),
		}
		return "pgx", target.String(), nil
	default:
		return "", "", errors.New("暂不支持该数据库类型")
	}
}

func credentialSecret(credentials Credentials) string {
	if credentials.Password != "" {
		return credentials.Password
	}
	return credentials.Token
}

func listTables(ctx context.Context, db *sql.DB, source Source, credentials Credentials) ([]Table, error) {
	var query string
	var args []any
	switch source.Driver {
	case DriverSQLite:
		query = `SELECT '', name, type, COALESCE(sql, '') FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name`
	case DriverMySQL:
		query = `SELECT table_schema, table_name, table_type, '' FROM information_schema.tables WHERE table_schema = ? ORDER BY table_name`
		args = []any{credentials.Database}
	case DriverPostgreSQL:
		query = `SELECT table_schema, table_name, table_type, '' FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema') ORDER BY table_schema, table_name`
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Table, 0)
	for rows.Next() {
		var table Table
		if err := rows.Scan(&table.Schema, &table.Name, &table.Type, &table.Definition); err != nil {
			return nil, err
		}
		result = append(result, table)
	}
	return result, rows.Err()
}

func enrichTable(ctx context.Context, db *sql.DB, driver Driver, table *Table) {
	switch driver {
	case DriverSQLite:
		var count int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+qualified(driver, table.Schema, table.Name)).Scan(&count); err == nil {
			table.RowCount = &count
		}
		var size sql.NullInt64
		if err := db.QueryRowContext(ctx, "SELECT SUM(pgsize) FROM dbstat WHERE name = ?", table.Name).Scan(&size); err == nil && size.Valid {
			table.SizeBytes = &size.Int64
		}
	case DriverMySQL:
		var rows, size sql.NullInt64
		var created sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT table_rows, data_length + index_length,
			COALESCE(DATE_FORMAT(create_time, '%Y-%m-%d %H:%i:%s'), '')
			FROM information_schema.tables WHERE table_schema = ? AND table_name = ?`,
			table.Schema, table.Name).Scan(&rows, &size, &created); err == nil {
			if rows.Valid {
				table.RowCount = &rows.Int64
			}
			if size.Valid {
				table.SizeBytes = &size.Int64
			}
			table.CreatedAt = created.String
		}
		var ignored, definition string
		if err := db.QueryRowContext(ctx, "SHOW CREATE TABLE "+qualified(driver, table.Schema, table.Name)).Scan(&ignored, &definition); err == nil {
			table.Definition = definition
		}
	case DriverPostgreSQL:
		var count, size sql.NullInt64
		if err := db.QueryRowContext(ctx, `SELECT c.reltuples::bigint, pg_total_relation_size(c.oid)
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = $1 AND c.relname = $2`, table.Schema, table.Name).Scan(&count, &size); err == nil {
			if count.Valid {
				table.RowCount = &count.Int64
			}
			if size.Valid {
				table.SizeBytes = &size.Int64
			}
		}
		if strings.EqualFold(table.Type, "VIEW") {
			_ = db.QueryRowContext(ctx, "SELECT pg_get_viewdef($1::regclass, true)",
				table.Schema+"."+table.Name).Scan(&table.Definition)
		}
	}
}

func listColumns(ctx context.Context, db *sql.DB, driver Driver, schema, table string) ([]Column, error) {
	if driver == DriverSQLite {
		rows, err := db.QueryContext(ctx, "PRAGMA table_info("+quote(driver, table)+")")
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		result := make([]Column, 0)
		for rows.Next() {
			var position, notNull, primaryKey int
			var name, dataType string
			var defaultValue any
			if err := rows.Scan(&position, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
				return nil, err
			}
			primary := primaryKey > 0
			result = append(result, Column{Name: name, DataType: dataType, Nullable: notNull == 0, PrimaryKey: primary, Default: normalizeValue(defaultValue), Position: position + 1})
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		primaryCount := 0
		for _, column := range result {
			if column.PrimaryKey {
				primaryCount++
			}
		}
		for index := range result {
			column := &result[index]
			identityGeneration := ""
			// SQLite's exact INTEGER PRIMARY KEY aliases the rowid only for a
			// single-column key. Composite integer keys are ordinary required
			// values and must never be inferred as generated.
			if primaryCount == 1 && column.PrimaryKey && strings.EqualFold(strings.TrimSpace(column.DataType), "INTEGER") {
				identityGeneration = "sqlite-rowid"
			}
			column.WriteMode = writeModeForColumn(column.PrimaryKey, column.DataType, column.Default, identityGeneration, "")
		}
		return result, nil
	}
	placeholderSchema, placeholderTable := "?", "?"
	if driver == DriverPostgreSQL {
		placeholderSchema, placeholderTable = "$1", "$2"
	}
	generatedSelect := "'' AS identity_generation, '' AS generated_kind"
	if driver == DriverMySQL {
		generatedSelect = "c.extra, '' AS generated_kind"
	} else if driver == DriverPostgreSQL {
		generatedSelect = "c.is_identity, c.is_generated"
	}
	query := `SELECT c.column_name, c.data_type, c.is_nullable, c.column_default, c.ordinal_position,
		CASE WHEN k.column_name IS NULL THEN 0 ELSE 1 END, ` + generatedSelect + `
		FROM information_schema.columns c
		LEFT JOIN (
			SELECT ku.table_schema, ku.table_name, ku.column_name
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage ku
			  ON tc.constraint_name = ku.constraint_name AND tc.table_schema = ku.table_schema
			WHERE tc.constraint_type = 'PRIMARY KEY'
		) k ON k.table_schema = c.table_schema AND k.table_name = c.table_name AND k.column_name = c.column_name
		WHERE c.table_schema = ` + placeholderSchema + ` AND c.table_name = ` + placeholderTable + `
		ORDER BY c.ordinal_position`
	rows, err := db.QueryContext(ctx, query, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Column, 0)
	for rows.Next() {
		var column Column
		var nullable string
		var primaryKey int
		var identityGeneration, generatedKind string
		if err := rows.Scan(&column.Name, &column.DataType, &nullable, &column.Default, &column.Position, &primaryKey, &identityGeneration, &generatedKind); err != nil {
			return nil, err
		}
		column.Nullable = nullable == "YES"
		column.PrimaryKey = primaryKey == 1
		column.Default = normalizeValue(column.Default)
		column.WriteMode = writeModeForColumn(column.PrimaryKey, column.DataType, column.Default, identityGeneration, generatedKind)
		result = append(result, column)
	}
	return result, rows.Err()
}

func loadTable(ctx context.Context, db *sql.DB, driver Driver, schema, name string) (Table, error) {
	if strings.TrimSpace(name) == "" {
		return Table{}, errors.New("数据表名称不能为空")
	}
	if driver == DriverPostgreSQL && schema == "" {
		schema = "public"
	}
	if driver == DriverMySQL && schema == "" {
		var err error
		if err = db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&schema); err != nil {
			return Table{}, err
		}
	}
	columns, err := listColumns(ctx, db, driver, schema, name)
	if err != nil {
		return Table{}, err
	}
	if len(columns) == 0 {
		return Table{}, errors.New("数据表不存在")
	}
	return Table{Schema: schema, Name: name, Type: "BASE TABLE", Columns: columns}, nil
}

func mutationValues(table Table, values map[string]any) ([]string, []any, error) {
	columns := make([]string, 0, len(values))
	for name := range values {
		if !hasColumn(table.Columns, name) {
			return nil, nil, fmt.Errorf("字段 %s 不存在", name)
		}
		columns = append(columns, name)
	}
	sort.Strings(columns)
	args := make([]any, len(columns))
	for index, name := range columns {
		args[index] = values[name]
	}
	return columns, args, nil
}

func primaryKeyWhere(driver Driver, table Table, keys map[string]any, offset int) (string, []any, error) {
	parts := make([]string, 0)
	args := make([]any, 0)
	for _, column := range table.Columns {
		if !column.PrimaryKey {
			continue
		}
		value, exists := keys[column.Name]
		if !exists {
			return "", nil, fmt.Errorf("缺少主键字段 %s", column.Name)
		}
		args = append(args, value)
		parts = append(parts, quote(driver, column.Name)+" = "+placeholder(driver, offset+len(args)))
	}
	if len(parts) == 0 {
		return "", nil, errors.New("该表没有主键，不能直接修改或删除行")
	}
	return strings.Join(parts, " AND "), args, nil
}

func execMutation(ctx context.Context, db *sql.DB, statement string, args []any) (MutationResult, error) {
	result, err := db.ExecContext(ctx, statement, args...)
	if err != nil {
		return MutationResult{}, err
	}
	affected, _ := result.RowsAffected()
	return MutationResult{RowsAffected: affected}, nil
}

func scanValues(rows *sql.Rows, count int, columnTypes ...[]*sql.ColumnType) ([]any, error) {
	raw := make([]any, count)
	targets := make([]any, count)
	for index := range raw {
		targets[index] = &raw[index]
	}
	if err := rows.Scan(targets...); err != nil {
		return nil, err
	}
	var types []*sql.ColumnType
	if len(columnTypes) > 0 {
		types = columnTypes[0]
	}
	for index := range raw {
		databaseType := ""
		if index < len(types) && types[index] != nil {
			databaseType = types[index].DatabaseTypeName()
		}
		raw[index] = normalizeDatabaseValue(raw[index], databaseType)
	}
	return raw, nil
}

func normalizeValue(value any) any {
	return normalizeDatabaseValue(value, "")
}

func normalizeDatabaseValue(value any, databaseType string) any {
	if isExactDecimalType(databaseType) {
		switch typed := value.(type) {
		case []byte:
			return string(typed)
		case string:
			return typed
		case float32:
			return strconv.FormatFloat(float64(typed), 'f', -1, 32)
		case float64:
			return strconv.FormatFloat(typed, 'f', -1, 64)
		case int:
			return strconv.Itoa(typed)
		case int8:
			return strconv.FormatInt(int64(typed), 10)
		case int16:
			return strconv.FormatInt(int64(typed), 10)
		case int32:
			return strconv.FormatInt(int64(typed), 10)
		case int64:
			return strconv.FormatInt(typed, 10)
		case uint:
			return strconv.FormatUint(uint64(typed), 10)
		case uint8:
			return strconv.FormatUint(uint64(typed), 10)
		case uint16:
			return strconv.FormatUint(uint64(typed), 10)
		case uint32:
			return strconv.FormatUint(uint64(typed), 10)
		case uint64:
			return strconv.FormatUint(typed, 10)
		}
	}
	switch typed := value.(type) {
	case []byte:
		if utf8.Valid(typed) {
			return string(typed)
		}
		return "base64:" + base64.StdEncoding.EncodeToString(typed)
	case time.Time:
		return formatDatabaseTime(typed, databaseType)
	case int64:
		if typed > maxSafeInteger || typed < -maxSafeInteger {
			return strconv.FormatInt(typed, 10)
		}
		return typed
	case uint64:
		if typed > maxSafeUnsignedInteger {
			return strconv.FormatUint(typed, 10)
		}
		return typed
	default:
		return value
	}
}

const (
	maxSafeInteger         int64  = 1<<53 - 1
	maxSafeUnsignedInteger uint64 = 1<<53 - 1
)

func isExactDecimalType(databaseType string) bool {
	typeName := strings.ToUpper(strings.TrimSpace(databaseType))
	return strings.Contains(typeName, "DECIMAL") || strings.Contains(typeName, "NUMERIC")
}

func formatDatabaseTime(value time.Time, databaseType string) string {
	typeName := strings.ToUpper(strings.TrimSpace(databaseType))
	switch {
	case typeName == "DATE":
		return value.Format("2006-01-02")
	case strings.Contains(typeName, "TIMESTAMPTZ"), strings.Contains(typeName, "WITH TIME ZONE"), strings.Contains(typeName, "DATETIMEOFFSET"):
		return value.Format(time.RFC3339Nano)
	case typeName == "TIME" || strings.HasPrefix(typeName, "TIME "):
		return value.Format("15:04:05.999999999")
	case typeName != "":
		// DATETIME and TIMESTAMP without an explicit zone are wall-clock
		// values. Do not append a misleading Z suffix that would make the UI
		// reinterpret them as instants.
		return value.Format("2006-01-02 15:04:05.999999999")
	default:
		return value.Format(time.RFC3339Nano)
	}
}

func rowsOrder(driver Driver, table Table, request RowsRequest) (string, Ordering) {
	primaryKeys := make([]string, 0)
	for _, column := range table.Columns {
		if column.PrimaryKey {
			primaryKeys = append(primaryKeys, column.Name)
		}
	}
	if request.SortColumn != "" && !hasColumn(table.Columns, request.SortColumn) {
		return "", Ordering{}
	}
	columns := make([]string, 0, len(primaryKeys)+1)
	orderParts := make([]string, 0, len(primaryKeys)+1)
	if request.SortColumn != "" {
		direction := "ASC"
		if strings.EqualFold(request.SortDirection, "desc") {
			direction = "DESC"
		}
		columns = append(columns, request.SortColumn)
		orderParts = append(orderParts, quote(driver, request.SortColumn)+" "+direction)
	}
	for _, primaryKey := range primaryKeys {
		if primaryKey == request.SortColumn {
			continue
		}
		columns = append(columns, primaryKey)
		orderParts = append(orderParts, quote(driver, primaryKey)+" ASC")
	}
	stable := len(primaryKeys) > 0
	// A table without a primary key may still be sorted for the first page,
	// but that ordering cannot make OFFSET pagination stable across writes.
	if len(orderParts) == 0 {
		return "", Ordering{Stable: stable, Columns: columns}
	}
	return " ORDER BY " + strings.Join(orderParts, ", "), Ordering{Stable: stable, Columns: columns}
}

func quote(driver Driver, identifier string) string {
	if driver == DriverMySQL {
		return "`" + strings.ReplaceAll(identifier, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func qualified(driver Driver, schema, table string) string {
	if schema == "" || driver == DriverSQLite {
		return quote(driver, table)
	}
	return quote(driver, schema) + "." + quote(driver, table)
}

func placeholder(driver Driver, position int) string {
	if driver == DriverPostgreSQL {
		return "$" + strconv.Itoa(position)
	}
	return "?"
}

func hasColumn(columns []Column, name string) bool {
	for _, column := range columns {
		if column.Name == name {
			return true
		}
	}
	return false
}

func writeModeForColumn(primaryKey bool, dataType string, defaultValue any, identityGeneration, generatedKind string) string {
	generation := strings.ToLower(strings.TrimSpace(identityGeneration))
	generated := generation == "yes" || generation == "sqlite-rowid" || strings.Contains(generation, "auto_increment") || strings.Contains(generation, "generated") ||
		(strings.ToLower(strings.TrimSpace(generatedKind)) != "never" && strings.TrimSpace(generatedKind) != "") ||
		strings.Contains(strings.ToLower(fmt.Sprint(defaultValue)), "nextval(")
	if generated {
		return "server-generated"
	}
	if defaultValue != nil && strings.TrimSpace(fmt.Sprint(defaultValue)) != "" {
		return "optional-default"
	}
	return "required"
}

func returnsRows(statement string) bool {
	fields := strings.Fields(strings.ToUpper(statement))
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "SELECT", "WITH", "PRAGMA", "EXPLAIN", "SHOW", "DESCRIBE", "DESC", "VALUES":
		return true
	default:
		return false
	}
}

func sourceID(driver Driver, location string) string {
	sum := sha256.Sum256([]byte(string(driver) + ":" + location))
	return string(driver) + "-" + fmt.Sprintf("%x", sum[:8])
}
