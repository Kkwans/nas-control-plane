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
	Token    string `json:"token,omitempty"`
	Database string `json:"database,omitempty"`
}

type Connection struct {
	SourceID    string      `json:"sourceId"`
	Credentials Credentials `json:"credentials,omitempty"`
}

// ConnectionDiagnostic is safe to return to the Server/API. It deliberately
// contains endpoint metadata, driver and stable outcome only; credentials and
// DSNs are never part of this result.
type ConnectionDiagnostic struct {
	Connected  bool   `json:"connected"`
	Code       string `json:"code,omitempty"`
	Driver     Driver `json:"driver"`
	Endpoint   string `json:"endpoint"`
	Database   string `json:"database,omitempty"`
	Operation  string `json:"operation"`
	DurationMs int64  `json:"durationMs"`
}

type TestConnectionRequest struct {
	Connection
}

// SavedCredential is the sanitized metadata exposed by the credential vault;
// encrypted bytes are kept inside the persistence adapter and are not API
// fields.
type SavedCredential struct {
	SourceID         string    `json:"sourceId"`
	Driver           Driver    `json:"driver"`
	Endpoint         string    `json:"endpoint"`
	Username         string    `json:"username,omitempty"`
	Database         string    `json:"database,omitempty"`
	KeyVersion       int       `json:"keyVersion"`
	AutomaticEnabled bool      `json:"automaticEnabled"`
	LastErrorCode    string    `json:"lastErrorCode,omitempty"`
	UpdatedAt        time.Time `json:"updatedAt"`
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
	Schema     string   `json:"schema"`
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Columns    []Column `json:"columns,omitempty"`
	RowCount   *int64   `json:"rowCount,omitempty"`
	SizeBytes  *int64   `json:"sizeBytes,omitempty"`
	CreatedAt  string   `json:"createdAt,omitempty"`
	Definition string   `json:"definition,omitempty"`
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
