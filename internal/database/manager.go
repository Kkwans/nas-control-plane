package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	mobycontainer "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
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
	now func() time.Time
}

func NewManager() *Manager {
	return &Manager{now: time.Now}
}

func (m *Manager) Discover(ctx context.Context) (Discovery, error) {
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
			Path: path, Status: "available", Tags: tags,
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

	if portOpen(ctx, "127.0.0.1:5432") {
		addSource(Source{
			ID: sourceID(DriverPostgreSQL, "127.0.0.1:5432/nasdb"), Name: "绿联 NAS 核心数据库",
			Driver: DriverPostgreSQL, Category: "system", Project: "绿联 NAS",
			Module: "共享文件夹、用户与系统服务", Location: "127.0.0.1:5432/nasdb",
			Host: "127.0.0.1", Port: 5432, DefaultDatabase: "nasdb",
			RequiresLogin: true, Status: "credentials_required", Tags: []string{"系统数据库", "PostgreSQL"},
		})
	}
	if portOpen(ctx, "127.0.0.1:3306") && !hasDriver(sources, DriverMySQL) {
		addSource(Source{
			ID: sourceID(DriverMySQL, "127.0.0.1:3306"), Name: "本机 MySQL 实例",
			Driver: DriverMySQL, Category: "project", Project: "未关联项目",
			Module: "尚未关联到具体项目", Location: "127.0.0.1:3306",
			Host: "127.0.0.1", Port: 3306, RequiresLogin: true,
			Status: "credentials_required", Tags: []string{"MySQL", "端口发现"},
		})
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
		return Catalog{}, err
	}
	for index := range tables {
		columns, columnErr := listColumns(ctx, db, source.Driver, tables[index].Schema, tables[index].Name)
		if columnErr != nil {
			return Catalog{}, columnErr
		}
		tables[index].Columns = columns
		enrichTable(ctx, db, source.Driver, &tables[index])
	}
	return Catalog{Source: source, Tables: tables}, nil
}

func (m *Manager) Query(ctx context.Context, request QueryRequest) (QueryResult, error) {
	statement := strings.TrimSpace(request.SQL)
	if statement == "" {
		return QueryResult{}, errors.New("SQL 语句不能为空")
	}
	_, db, err := m.connect(ctx, request.Connection)
	if err != nil {
		return QueryResult{}, err
	}
	defer db.Close()
	started := time.Now()
	if !returnsRows(statement) {
		result, execErr := db.ExecContext(ctx, statement)
		if execErr != nil {
			return QueryResult{}, execErr
		}
		affected, _ := result.RowsAffected()
		return QueryResult{Columns: []string{}, Rows: [][]any{}, RowsAffected: affected, DurationMs: time.Since(started).Milliseconds()}, nil
	}
	rows, err := db.QueryContext(ctx, statement)
	if err != nil {
		return QueryResult{}, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return QueryResult{}, err
	}
	result := QueryResult{Columns: columns, Rows: make([][]any, 0), DurationMs: 0}
	for rows.Next() {
		if len(result.Rows) >= maxQueryRows {
			result.Truncated = true
			break
		}
		values, scanErr := scanValues(rows, len(columns))
		if scanErr != nil {
			return QueryResult{}, scanErr
		}
		result.Rows = append(result.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return QueryResult{}, err
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
		return RowsResult{}, err
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
	order := ""
	if request.SortColumn != "" {
		if !hasColumn(table.Columns, request.SortColumn) {
			return RowsResult{}, errors.New("排序字段不存在")
		}
		direction := "ASC"
		if strings.EqualFold(request.SortDirection, "desc") {
			direction = "DESC"
		}
		order = " ORDER BY " + quote(source.Driver, request.SortColumn) + " " + direction
	}
	query := "SELECT * FROM " + qualified(source.Driver, table.Schema, table.Name) + order +
		" LIMIT " + strconv.Itoa(limit+1) + " OFFSET " + strconv.Itoa(request.Offset)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return RowsResult{}, err
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return RowsResult{}, err
	}
	result := RowsResult{Table: table, Rows: make([]Row, 0, limit), Limit: limit, Offset: request.Offset}
	for rows.Next() {
		values, scanErr := scanValues(rows, len(names))
		if scanErr != nil {
			return RowsResult{}, scanErr
		}
		if len(result.Rows) == limit {
			result.HasMore = true
			break
		}
		row := make(Row, len(names))
		for index, name := range names {
			row[name] = values[index]
		}
		result.Rows = append(result.Rows, row)
	}
	return result, rows.Err()
}

func (m *Manager) Insert(ctx context.Context, request InsertRequest) (MutationResult, error) {
	source, db, err := m.connect(ctx, request.Connection)
	if err != nil {
		return MutationResult{}, err
	}
	defer db.Close()
	table, err := loadTable(ctx, db, source.Driver, request.Schema, request.Table)
	if err != nil {
		return MutationResult{}, err
	}
	columns, args, err := mutationValues(table, request.Values)
	if err != nil {
		return MutationResult{}, err
	}
	placeholders := make([]string, len(columns))
	for index := range placeholders {
		placeholders[index] = placeholder(source.Driver, index+1)
		columns[index] = quote(source.Driver, columns[index])
	}
	statement := "INSERT INTO " + qualified(source.Driver, table.Schema, table.Name) +
		" (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	return execMutation(ctx, db, statement, args)
}

func (m *Manager) Update(ctx context.Context, request UpdateRequest) (MutationResult, error) {
	source, db, err := m.connect(ctx, request.Connection)
	if err != nil {
		return MutationResult{}, err
	}
	defer db.Close()
	table, err := loadTable(ctx, db, source.Driver, request.Schema, request.Table)
	if err != nil {
		return MutationResult{}, err
	}
	columns, args, err := mutationValues(table, request.Values)
	if err != nil {
		return MutationResult{}, err
	}
	setParts := make([]string, len(columns))
	for index, column := range columns {
		setParts[index] = quote(source.Driver, column) + " = " + placeholder(source.Driver, index+1)
	}
	where, keyArgs, err := primaryKeyWhere(source.Driver, table, request.Keys, len(args))
	if err != nil {
		return MutationResult{}, err
	}
	args = append(args, keyArgs...)
	statement := "UPDATE " + qualified(source.Driver, table.Schema, table.Name) +
		" SET " + strings.Join(setParts, ", ") + " WHERE " + where
	return execMutation(ctx, db, statement, args)
}

func (m *Manager) Delete(ctx context.Context, request DeleteRequest) (MutationResult, error) {
	source, db, err := m.connect(ctx, request.Connection)
	if err != nil {
		return MutationResult{}, err
	}
	defer db.Close()
	table, err := loadTable(ctx, db, source.Driver, request.Schema, request.Table)
	if err != nil {
		return MutationResult{}, err
	}
	where, args, err := primaryKeyWhere(source.Driver, table, request.Keys, 0)
	if err != nil {
		return MutationResult{}, err
	}
	statement := "DELETE FROM " + qualified(source.Driver, table.Schema, table.Name) + " WHERE " + where
	return execMutation(ctx, db, statement, args)
}

func (m *Manager) connect(ctx context.Context, connection Connection) (Source, *sql.DB, error) {
	discovery, err := m.Discover(ctx)
	if err != nil {
		return Source{}, nil, err
	}
	var source Source
	for _, candidate := range discovery.Sources {
		if candidate.ID == connection.SourceID {
			source = candidate
			break
		}
	}
	if source.ID == "" {
		return Source{}, nil, errors.New("数据库来源不存在或已离线")
	}
	driver, dsn, err := connectionString(source, connection.Credentials)
	if err != nil {
		return Source{}, nil, err
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return Source{}, nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return Source{}, nil, err
	}
	return source, db, nil
}

func connectionString(source Source, credentials Credentials) (string, string, error) {
	switch source.Driver {
	case DriverSQLite:
		return "sqlite", source.Path, nil
	case DriverMySQL:
		if credentials.Username == "" || credentials.Database == "" {
			return "", "", errors.New("MySQL 用户名和数据库名不能为空")
		}
		config := url.Values{}
		config.Set("parseTime", "true")
		config.Set("charset", "utf8mb4")
		return "mysql", credentials.Username + ":" + credentials.Password + "@tcp(" +
			net.JoinHostPort(source.Host, strconv.Itoa(source.Port)) + ")/" +
			url.PathEscape(credentials.Database) + "?" + config.Encode(), nil
	case DriverPostgreSQL:
		if credentials.Username == "" {
			return "", "", errors.New("PostgreSQL 用户名不能为空")
		}
		databaseName := credentials.Database
		if databaseName == "" {
			databaseName = source.DefaultDatabase
		}
		values := url.Values{"sslmode": []string{"disable"}}
		target := &url.URL{
			Scheme: "postgres", User: url.UserPassword(credentials.Username, credentials.Password),
			Host: net.JoinHostPort(source.Host, strconv.Itoa(source.Port)), Path: databaseName,
			RawQuery: values.Encode(),
		}
		return "pgx", target.String(), nil
	default:
		return "", "", errors.New("暂不支持该数据库类型")
	}
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
			result = append(result, Column{Name: name, DataType: dataType, Nullable: notNull == 0, PrimaryKey: primaryKey > 0, Default: normalizeValue(defaultValue), Position: position + 1})
		}
		return result, rows.Err()
	}
	placeholderSchema, placeholderTable := "?", "?"
	if driver == DriverPostgreSQL {
		placeholderSchema, placeholderTable = "$1", "$2"
	}
	query := `SELECT c.column_name, c.data_type, c.is_nullable, c.column_default, c.ordinal_position,
		CASE WHEN k.column_name IS NULL THEN 0 ELSE 1 END
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
		if err := rows.Scan(&column.Name, &column.DataType, &nullable, &column.Default, &column.Position, &primaryKey); err != nil {
			return nil, err
		}
		column.Nullable = nullable == "YES"
		column.PrimaryKey = primaryKey == 1
		column.Default = normalizeValue(column.Default)
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
	if len(values) == 0 {
		return nil, nil, errors.New("没有需要写入的字段")
	}
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

func scanValues(rows *sql.Rows, count int) ([]any, error) {
	raw := make([]any, count)
	targets := make([]any, count)
	for index := range raw {
		targets[index] = &raw[index]
	}
	if err := rows.Scan(targets...); err != nil {
		return nil, err
	}
	for index := range raw {
		raw[index] = normalizeValue(raw[index])
	}
	return raw, nil
}

func normalizeValue(value any) any {
	switch typed := value.(type) {
	case []byte:
		if utf8.Valid(typed) {
			return string(typed)
		}
		return "base64:" + base64.StdEncoding.EncodeToString(typed)
	case time.Time:
		return typed.Format(time.RFC3339Nano)
	default:
		return value
	}
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

func hasDriver(sources []Source, driver Driver) bool {
	for _, source := range sources {
		if source.Driver == driver {
			return true
		}
	}
	return false
}

func discoverDockerDatabases(ctx context.Context) ([]Source, []sqliteCandidate) {
	apiClient, err := client.New(client.WithHost(localDockerHost), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, nil
	}
	defer apiClient.Close()
	response, err := apiClient.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, nil
	}

	sources := make([]Source, 0)
	candidates := make([]sqliteCandidate, 0)
	for _, item := range response.Items {
		inspection, inspectErr := apiClient.ContainerInspect(ctx, item.ID, client.ContainerInspectOptions{})
		if inspectErr != nil || inspection.Container.Config == nil {
			continue
		}
		labels := inspection.Container.Config.Labels
		project := strings.TrimSpace(labels["com.docker.compose.project"])
		service := strings.TrimSpace(labels["com.docker.compose.service"])
		if project == "" {
			project = strings.TrimPrefix(inspection.Container.Name, "/")
		}
		if service == "" {
			service = "数据库服务"
		}

		image := strings.ToLower(inspection.Container.Config.Image)
		if driver, port, ok := databaseImage(image); ok {
			publicPort := publishedDatabasePort(item.Ports, port)
			if publicPort > 0 {
				defaultDatabase := safeEnvironmentValue(inspection.Container.Config.Env, defaultDatabaseKey(driver))
				location := "127.0.0.1:" + strconv.Itoa(publicPort)
				if defaultDatabase != "" {
					location += "/" + defaultDatabase
				}
				sources = append(sources, Source{
					ID: sourceID(driver, project+":"+location), Name: project + " · " + driverName(driver),
					Driver: driver, Category: "project", Project: project, Module: service + " 容器",
					Location: location, Host: "127.0.0.1", Port: publicPort,
					DefaultDatabase: defaultDatabase, RequiresLogin: true, Status: "credentials_required",
					Tags: []string{"容器发现", driverName(driver)},
				})
			}
		}

		for _, environment := range inspection.Container.Config.Env {
			key, value, found := strings.Cut(environment, "=")
			if !found || (!strings.Contains(key, "DATASOURCE_URL") && key != "DATABASE_URL") {
				continue
			}
			if source, ok := sourceFromDatabaseURL(value, project, service); ok {
				sources = append(sources, source)
			}
		}

		for _, mount := range inspection.Container.Mounts {
			destination := strings.ToLower(mount.Destination)
			if mount.Source == "" || (!strings.Contains(destination, "data") &&
				!strings.Contains(destination, "database") && !strings.Contains(destination, "config") &&
				!strings.Contains(destination, "support")) {
				continue
			}
			candidates = append(candidates, sqliteCandidate{
				root: mount.Source, project: project, module: service + " 容器数据",
			})
		}
	}
	return sources, candidates
}

func databaseImage(image string) (Driver, int, bool) {
	switch {
	case strings.Contains(image, "mariadb"), strings.Contains(image, "mysql"):
		return DriverMySQL, 3306, true
	case strings.Contains(image, "postgres"):
		return DriverPostgreSQL, 5432, true
	default:
		return "", 0, false
	}
}

func publishedDatabasePort(ports []mobycontainer.PortSummary, privatePort int) int {
	for _, port := range ports {
		if int(port.PrivatePort) == privatePort && port.PublicPort > 0 {
			return int(port.PublicPort)
		}
	}
	if portOpen(context.Background(), "127.0.0.1:"+strconv.Itoa(privatePort)) {
		return privatePort
	}
	return 0
}

func defaultDatabaseKey(driver Driver) string {
	if driver == DriverPostgreSQL {
		return "POSTGRES_DB"
	}
	return "MYSQL_DATABASE"
}

func safeEnvironmentValue(environment []string, expectedKey string) string {
	for _, item := range environment {
		key, value, found := strings.Cut(item, "=")
		if found && key == expectedKey {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sourceFromDatabaseURL(value, project, service string) (Source, bool) {
	normalized := strings.TrimSpace(strings.TrimPrefix(value, "jdbc:"))
	parsed, err := url.Parse(normalized)
	if err != nil {
		return Source{}, false
	}
	var driver Driver
	var defaultPort int
	switch parsed.Scheme {
	case "mysql", "mariadb":
		driver, defaultPort = DriverMySQL, 3306
	case "postgres", "postgresql":
		driver, defaultPort = DriverPostgreSQL, 5432
	default:
		return Source{}, false
	}
	databaseName := strings.Trim(strings.TrimSpace(parsed.Path), "/")
	if databaseName == "" {
		return Source{}, false
	}
	host := parsed.Hostname()
	if host == "" || host == "localhost" {
		host = "127.0.0.1"
	}
	port := defaultPort
	if parsed.Port() != "" {
		if parsedPort, parseErr := strconv.Atoi(parsed.Port()); parseErr == nil {
			port = parsedPort
		}
	}
	location := host + ":" + strconv.Itoa(port) + "/" + databaseName
	return Source{
		ID: sourceID(driver, project+":"+location), Name: project + " · " + databaseName,
		Driver: driver, Category: "project", Project: project, Module: service + " 使用的数据库",
		Location: location, Host: host, Port: port, DefaultDatabase: databaseName,
		RequiresLogin: true, Status: "credentials_required", Tags: []string{"项目配置发现", driverName(driver)},
	}, true
}

func driverName(driver Driver) string {
	switch driver {
	case DriverMySQL:
		return "MySQL"
	case DriverPostgreSQL:
		return "PostgreSQL"
	default:
		return "SQLite"
	}
}

func portOpen(ctx context.Context, address string) bool {
	dialer := net.Dialer{Timeout: 150 * time.Millisecond}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func isSQLiteFile(ctx context.Context, path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return false
	}
	defer db.Close()
	var count int
	return db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master").Scan(&count) == nil
}

func discoverSQLiteFiles(ctx context.Context, root string) []string {
	result := make([]string, 0)
	visited := 0
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || ctx.Err() != nil {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		depth := len(strings.Split(filepath.ToSlash(relative), "/"))
		if entry.IsDir() {
			name := strings.ToLower(entry.Name())
			if depth > 5 || strings.HasPrefix(name, ".git") || name == "node_modules" ||
				name == ".pnpm-store" || name == ".codex-backups" || name == "#recycle" ||
				name == "backup" || name == "backups" {
				return filepath.SkipDir
			}
			return nil
		}
		visited++
		if visited > maxScanFiles {
			return fs.SkipAll
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if extension != ".db" && extension != ".sqlite" && extension != ".sqlite3" {
			return nil
		}
		if isSQLiteFile(ctx, path) {
			result = append(result, path)
		}
		return nil
	})
	return result
}

func associationForPath(path string) (string, string) {
	normalized := filepath.ToSlash(path)
	for _, prefix := range []string{"/volume2/Project/", "/volume2/DockerProject/"} {
		if strings.HasPrefix(normalized, prefix) {
			remainder := strings.TrimPrefix(normalized, prefix)
			project := strings.SplitN(remainder, "/", 2)[0]
			return project, "项目数据库文件"
		}
	}
	return "未关联项目", "自动发现的 SQLite 数据库"
}
