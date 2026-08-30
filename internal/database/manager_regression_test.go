package database

import (
	"context"
	"testing"
	"time"

	mobycontainer "github.com/moby/moby/api/types/container"
)

func TestRowsOrderUsesPrimaryKeyTieBreakers(t *testing.T) {
	table := Table{Columns: []Column{
		{Name: "name", Position: 1},
		{Name: "tenant_id", PrimaryKey: true, Position: 2},
		{Name: "id", PrimaryKey: true, Position: 3},
	}}
	order, metadata := rowsOrder(DriverSQLite, table, RowsRequest{})
	if order != ` ORDER BY "tenant_id" ASC, "id" ASC` {
		t.Fatalf("default order = %q", order)
	}
	if !metadata.Stable || len(metadata.Columns) != 2 || metadata.Columns[0] != "tenant_id" {
		t.Fatalf("ordering metadata = %#v", metadata)
	}
	order, metadata = rowsOrder(DriverSQLite, table, RowsRequest{SortColumn: "name", SortDirection: "desc"})
	if order != ` ORDER BY "name" DESC, "tenant_id" ASC, "id" ASC` || !metadata.Stable {
		t.Fatalf("explicit order = %q %#v", order, metadata)
	}
	_, metadata = rowsOrder(DriverSQLite, Table{Columns: []Column{{Name: "value"}}}, RowsRequest{})
	if metadata.Stable {
		t.Fatal("table without a primary key reported stable pagination")
	}
	if _, invalid := rowsOrder(DriverSQLite, table, RowsRequest{SortColumn: "missing"}); invalid.Columns != nil {
		t.Fatalf("invalid sort metadata = %#v", invalid)
	}
}

func TestNormalizeDatabaseValueKeepsWirePrecision(t *testing.T) {
	if got := normalizeDatabaseValue(int64(1<<53-1), "BIGINT"); got != int64(1<<53-1) {
		t.Fatalf("safe bigint = %#v", got)
	}
	if got := normalizeDatabaseValue(int64(1<<53), "BIGINT"); got != "9007199254740992" {
		t.Fatalf("unsafe bigint = %#v", got)
	}
	if got := normalizeDatabaseValue(uint64(^uint64(0)), "BIGINT"); got != "18446744073709551615" {
		t.Fatalf("unsafe unsigned bigint = %#v", got)
	}
	if got := normalizeDatabaseValue(float64(123.4500), "DECIMAL(20,4)"); got != "123.45" {
		t.Fatalf("decimal = %#v", got)
	}
}

func TestNormalizeDatabaseTimeKeepsTimezoneSemantics(t *testing.T) {
	value := time.Date(2026, time.August, 30, 12, 34, 56, 123000000, time.FixedZone("UTC+8", 8*60*60))
	if got := normalizeDatabaseValue(value, "TIMESTAMP"); got != "2026-08-30 12:34:56.123" {
		t.Fatalf("timezone-naive timestamp = %#v", got)
	}
	if got := normalizeDatabaseValue(value, "TIMESTAMPTZ"); got != "2026-08-30T12:34:56.123+08:00" {
		t.Fatalf("timezone-aware timestamp = %#v", got)
	}
	if got := normalizeDatabaseValue(value, "DATE"); got != "2026-08-30" {
		t.Fatalf("date = %#v", got)
	}
}

func TestWriteModeDoesNotGuessNonGeneratedIntegerKeys(t *testing.T) {
	if got := writeModeForColumn(true, "INT", nil, "", ""); got != "required" {
		t.Fatalf("integer primary key write mode = %q", got)
	}
	if got := writeModeForColumn(true, "INTEGER", nil, "sqlite-rowid", ""); got != "server-generated" {
		t.Fatalf("sqlite rowid write mode = %q", got)
	}
}

func TestCompositeIntegerKeysRemainRequired(t *testing.T) {
	columns := []Column{
		{Name: "tenant_id", DataType: "INTEGER", PrimaryKey: true},
		{Name: "item_id", DataType: "INTEGER", PrimaryKey: true},
	}
	for _, column := range columns {
		if got := writeModeForColumn(column.PrimaryKey, column.DataType, column.Default, "", ""); got != "required" {
			t.Fatalf("composite key %s write mode = %q", column.Name, got)
		}
	}
}

func TestPublishedDatabasePortDoesNotInferContainerMapping(t *testing.T) {
	if got := publishedDatabasePort([]mobycontainer.PortSummary{{PrivatePort: 3306, PublicPort: 0}}, 3306); got != 0 {
		t.Fatalf("unpublished port = %d", got)
	}
	if got := publishedDatabasePort([]mobycontainer.PortSummary{{PrivatePort: 3306, PublicPort: 13306, Type: "udp"}}, 3306); got != 0 {
		t.Fatalf("udp database port = %d", got)
	}
	if got := publishedDatabasePort([]mobycontainer.PortSummary{{PrivatePort: 3306, PublicPort: 13306}}, 3306); got != 13306 {
		t.Fatalf("published port = %d", got)
	}
}

func TestDatabaseURLServiceHostnameIsContainerInternal(t *testing.T) {
	source, ok := sourceFromDatabaseURL("postgres://postgres:secret@db:5432/control", "stack", "api")
	if !ok || source.Reachability != "container-internal" || source.Evidence != "database-url" || source.Host != "db" || source.Port != 5432 {
		t.Fatalf("source = %#v", source)
	}
	if source.Status != "unreachable" {
		t.Fatalf("status = %q", source.Status)
	}
}

func TestDiscoveryRegistryUsesTTLAndForceRefresh(t *testing.T) {
	clock := time.Unix(100, 0)
	calls := 0
	manager := NewManager()
	manager.now = func() time.Time { return clock }
	manager.discoverFn = func(context.Context) (Discovery, error) {
		calls++
		return Discovery{CollectedAt: clock, Sources: []Source{{ID: "source"}}}, nil
	}
	if _, err := manager.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("cached discovery calls = %d", calls)
	}
	if _, err := manager.DiscoverWithOptions(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("forced discovery calls = %d", calls)
	}
}
