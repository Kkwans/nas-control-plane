package database

import "time"

type Driver string

const (
	DriverSQLite     Driver = "sqlite"
	DriverMySQL      Driver = "mysql"
	DriverPostgreSQL Driver = "postgresql"
)

type Source struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Driver          Driver   `json:"driver"`
	Category        string   `json:"category"`
	Project         string   `json:"project"`
	Module          string   `json:"module"`
	Location        string   `json:"location"`
	Path            string   `json:"path,omitempty"`
	Host            string   `json:"host,omitempty"`
	Port            int      `json:"port,omitempty"`
	DefaultDatabase string   `json:"defaultDatabase,omitempty"`
	RequiresLogin   bool     `json:"requiresLogin"`
	Status          string   `json:"status"`
	Tags            []string `json:"tags"`
}

type Discovery struct {
	CollectedAt time.Time `json:"collectedAt"`
	Sources     []Source  `json:"sources"`
}

type Credentials struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Database string `json:"database,omitempty"`
}

type Connection struct {
	SourceID    string      `json:"sourceId"`
	Credentials Credentials `json:"credentials,omitempty"`
}

type Column struct {
	Name       string `json:"name"`
	DataType   string `json:"dataType"`
	Nullable   bool   `json:"nullable"`
	PrimaryKey bool   `json:"primaryKey"`
	Default    any    `json:"default,omitempty"`
	Position   int    `json:"position"`
}

type Table struct {
	Schema  string   `json:"schema"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Columns []Column `json:"columns,omitempty"`
}

type CatalogRequest struct {
	Connection
}

type Catalog struct {
	Source Source  `json:"source"`
	Tables []Table `json:"tables"`
}

type QueryRequest struct {
	Connection
	SQL string `json:"sql"`
}

type QueryResult struct {
	Columns      []string `json:"columns"`
	Rows         [][]any  `json:"rows"`
	RowsAffected int64    `json:"rowsAffected"`
	Truncated    bool     `json:"truncated"`
	DurationMs   int64    `json:"durationMs"`
}

type RowsRequest struct {
	Connection
	Schema        string `json:"schema,omitempty"`
	Table         string `json:"table"`
	Limit         int    `json:"limit,omitempty"`
	Offset        int    `json:"offset,omitempty"`
	SortColumn    string `json:"sortColumn,omitempty"`
	SortDirection string `json:"sortDirection,omitempty"`
}

type RowsResult struct {
	Table   Table `json:"table"`
	Rows    []Row `json:"rows"`
	Limit   int   `json:"limit"`
	Offset  int   `json:"offset"`
	HasMore bool  `json:"hasMore"`
}

type Row map[string]any

type InsertRequest struct {
	Connection
	Schema string         `json:"schema,omitempty"`
	Table  string         `json:"table"`
	Values map[string]any `json:"values"`
}

type UpdateRequest struct {
	InsertRequest
	Keys map[string]any `json:"keys"`
}

type DeleteRequest struct {
	Connection
	Schema string         `json:"schema,omitempty"`
	Table  string         `json:"table"`
	Keys   map[string]any `json:"keys"`
}

type MutationResult struct {
	RowsAffected int64 `json:"rowsAffected"`
}
