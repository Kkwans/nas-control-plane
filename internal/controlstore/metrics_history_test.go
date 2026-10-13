package controlstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

func historyInsertParent(t *testing.T, s *Store, at time.Time) {
	t.Helper()
	if _, err := s.database.Exec(`INSERT INTO metric_samples VALUES (?, 1, 2, 3, 4, 5, 6, 7, 8)`, at.Unix()); err != nil {
		t.Fatal(err)
	}
}

func historyInsertTemperatures(t *testing.T, s *Store, at time.Time, count int) {
	t.Helper()
	if _, err := s.database.Exec(`WITH RECURSIVE temperatures(n) AS
 (SELECT 1 UNION ALL SELECT n+1 FROM temperatures WHERE n < ?)
 INSERT INTO metric_temperature_samples SELECT ?, printf('sensor-%06d', n), 20 FROM temperatures`, count, at.Unix()); err != nil {
		t.Fatal(err)
	}
}

func TestMetricsHistoryExactBoundsAndSelectedParents(t *testing.T) {
	s := metricsTestStore(t)
	at := time.Date(2026, 10, 13, 1, 2, 0, 0, time.UTC)
	for _, delta := range []time.Duration{-time.Second, 0, time.Second, 2 * time.Second, 3 * time.Second, 24 * time.Hour} {
		historyInsertParent(t, s, at.Add(delta))
		if delta != 24*time.Hour {
			historyInsertTemperatures(t, s, at.Add(delta), 1)
		}
	}
	// 区间内无父记录的孤儿及未来超大集合均不得影响本次结果或预算。
	historyInsertTemperatures(t, s, at.Add(4*time.Second), MaxMetricHistoryTemperatures+1)
	historyInsertTemperatures(t, s, at.Add(24*time.Hour), MaxMetricHistoryTemperatures+1)
	cases := []struct {
		name     string
		from, to time.Time
		want     []int64
	}{
		{"closed", at, at.Add(2 * time.Second), []int64{at.Unix(), at.Unix() + 1, at.Unix() + 2}},
		{"in-range orphan", at, at.Add(4 * time.Second), []int64{at.Unix(), at.Unix() + 1, at.Unix() + 2, at.Unix() + 3}},
		{"fractional lower", at.Add(time.Nanosecond), at.Add(2 * time.Second), []int64{at.Unix() + 1, at.Unix() + 2}},
		{"fractional upper", at, at.Add(2*time.Second - time.Nanosecond), []int64{at.Unix(), at.Unix() + 1}},
		{"no integer timestamp", at.Add(time.Nanosecond), at.Add(time.Second - time.Nanosecond), nil},
		{"single inclusive point", at, at, []int64{at.Unix()}},
		{"non UTC", at.In(time.FixedZone("offset", 8*3600)), at.Add(time.Second), []int64{at.Unix(), at.Unix() + 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			samples, err := s.MetricSamplesInRange(ctx, tc.from, tc.to)
			if err != nil || len(samples) != len(tc.want) {
				t.Fatalf("samples=%d want=%d error=%v", len(samples), len(tc.want), err)
			}
			for i, sample := range samples {
				if sample.CollectedAt.Unix() != tc.want[i] || len(sample.Temperatures) != 1 {
					t.Fatalf("sample %d: %+v", i, sample)
				}
			}
		})
	}
}

func TestMetricsHistoryPreEpochFractionalBounds(t *testing.T) {
	s := metricsTestStore(t)
	for _, sec := range []int64{-2, -1, 0} {
		historyInsertParent(t, s, time.Unix(sec, 0))
	}
	got, err := s.MetricSamplesInRange(context.Background(), time.Unix(-2, 1), time.Unix(-1, 999999999))
	if err != nil || len(got) != 1 || got[0].CollectedAt.Unix() != -1 {
		t.Fatalf("pre-epoch result=%v error=%v", got, err)
	}
}

func TestMetricsHistoryParentLimitAndLegacyPreservation(t *testing.T) {
	s := metricsTestStore(t)
	at := time.Date(2026, 10, 13, 1, 2, 0, 0, time.UTC)
	if _, err := s.database.Exec(`WITH RECURSIVE buckets(n) AS
 (SELECT 0 UNION ALL SELECT n+1 FROM buckets WHERE n < ?)
 INSERT INTO metric_samples SELECT ?+n,0,0,0,0,0,0,0,0 FROM buckets`, MaxMetricSamples, at.Unix()); err != nil {
		t.Fatal(err)
	}
	// 旧数据可有秒级父桶；精确到上限仍完整成功，多一个必须明确失败。
	got, err := s.MetricSamplesInRange(context.Background(), at, at.Add(time.Duration(MaxMetricSamples-1)*time.Second))
	if err != nil || len(got) != MaxMetricSamples {
		t.Fatalf("exact parents=%d error=%v", len(got), err)
	}
	got, err = s.MetricSamplesInRange(context.Background(), at, at.Add(time.Duration(MaxMetricSamples)*time.Second))
	if !errors.Is(err, ErrMetricHistoryLimitExceeded) || got != nil {
		t.Fatalf("over parents=%d error=%v", len(got), err)
	}
	if metricsCount(t, s, "metric_samples") != MaxMetricSamples+1 {
		t.Fatal("read changed retained parents")
	}
}

func TestMetricsHistoryTemperatureLimitAndLegacyPreservation(t *testing.T) {
	s := metricsTestStore(t)
	at := time.Date(2026, 10, 13, 1, 2, 0, 0, time.UTC)
	historyInsertParent(t, s, at)
	historyInsertTemperatures(t, s, at, MaxMetricHistoryTemperatures)
	got, err := s.MetricSamplesInRange(context.Background(), at, at)
	if err != nil || len(got) != 1 || len(got[0].Temperatures) != MaxMetricHistoryTemperatures {
		t.Fatalf("exact temperature result=%d error=%v", len(got), err)
	}
	// 历史不能套用新写入每桶 128 上限：预算内的旧大集合完整保留。
	if _, err := MarshalMetricHistory(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	if _, err := s.database.Exec(`INSERT INTO metric_temperature_samples VALUES (?, 'one-more', 21)`, at.Unix()); err != nil {
		t.Fatal(err)
	}
	got, err = s.MetricSamplesInRange(context.Background(), at, at)
	if !errors.Is(err, ErrMetricHistoryLimitExceeded) || got != nil {
		t.Fatalf("over temperature result=%d error=%v", len(got), err)
	}
	if err := s.PruneMetricSamples(context.Background(), at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if metricsCount(t, s, "metric_temperature_samples") != MaxMetricHistoryTemperatures+1 {
		t.Fatal("oversized retained legacy sensors were changed")
	}
}

func TestMetricsHistoryResponseBytesExactAndRefused(t *testing.T) {
	s := metricsTestStore(t)
	at := time.Date(2026, 10, 13, 1, 2, 0, 0, time.UTC)
	historyInsertParent(t, s, at)
	sample := MetricSample{CollectedAt: at, CPUPercent: 1, MemoryPercent: 2, Load1: 3, DiskPercent: 4,
		NetworkReceive: 5, NetworkTransmit: 6, DiskReadBytes: 7, DiskWriteBytes: 8,
		Temperatures: []MetricTemperature{{Name: "", TemperatureCelsius: 20}}}
	base, err := MarshalMetricHistory(context.Background(), []MetricSample{sample})
	if err != nil {
		t.Fatal(err)
	}
	name := strings.Repeat("x", MaxMetricHistoryResponseBytes-len(base))
	for _, extra := range []string{"", "x"} {
		if _, err := s.database.Exec(`DELETE FROM metric_temperature_samples`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.database.Exec(`INSERT INTO metric_temperature_samples VALUES (?, ?, 20)`, at.Unix(), name+extra); err != nil {
			t.Fatal(err)
		}
		got, err := s.MetricSamplesInRange(context.Background(), at, at)
		if extra != "" {
			if !errors.Is(err, ErrMetricHistoryLimitExceeded) || got != nil {
				t.Fatalf("byte overrun: result=%d error=%v", len(got), err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := MarshalMetricHistory(context.Background(), got)
		if err != nil || len(encoded) != MaxMetricHistoryResponseBytes {
			t.Fatalf("exact encoding bytes=%d error=%v", len(encoded), err)
		}
	}
	if metricsCount(t, s, "metric_temperature_samples") != 1 {
		t.Fatal("byte limit deleted legacy value")
	}
	// SQLite length(TEXT) 会在 NUL 处结束；必须按 BLOB 字节长度拦截。
	if _, err := s.database.Exec(`UPDATE metric_temperature_samples SET sensor_name = CAST(zeroblob(?) AS TEXT)`, MaxMetricHistoryResponseBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MetricSamplesInRange(context.Background(), at, at); !errors.Is(err, ErrMetricHistoryLimitExceeded) {
		t.Fatalf("NUL long name limit error=%v", err)
	}
}

func TestMetricsHistoryBudgetMatchesJSONEscapingAndSevenDays(t *testing.T) {
	at := time.Date(2026, 10, 13, 1, 2, 0, 0, time.UTC)
	var allCharacters strings.Builder
	for r := rune(0); r <= 0x10ffff; r++ {
		if r < 0xd800 || r > 0xdfff {
			allCharacters.WriteRune(r)
		}
	}
	for _, name := range []string{"", `quotes"and\slashes`, "\x00\x01\b\f\n\r\t<>&\u2028\u2029温度🌡️", allCharacters.String()} {
		budget := newMetricHistoryBudget()
		sample := MetricSample{CollectedAt: at, CPUPercent: math.MaxFloat64, Temperatures: []MetricTemperature{{name, -1.23e-8}, {"next", 20}}}
		if err := budget.addSample(sample); err != nil {
			t.Fatal(err)
		}
		for i, temperature := range sample.Temperatures {
			if err := budget.addTemperature(temperature, i == 0); err != nil {
				t.Fatal(err)
			}
		}
		encoded, err := json.Marshal([]MetricSample{sample})
		if err != nil || budget.bytes != len(encoded)+1 {
			t.Fatalf("escaped size got=%d want=%d error=%v", budget.bytes, len(encoded)+1, err)
		}
	}
	// 明确预算实例：正常七天六个短名称传感器可完整编码，七个超总行数。
	for _, sensors := range []int{6, 7} {
		samples := make([]MetricSample, MaxMetricSamples)
		for i := range samples {
			samples[i] = MetricSample{CollectedAt: at.Add(time.Duration(i) * time.Minute), CPUPercent: 19.25, NetworkReceive: 123456789}
			for sensor := 0; sensor < sensors; sensor++ {
				samples[i].Temperatures = append(samples[i].Temperatures, MetricTemperature{fmt.Sprintf("coretemp-%d", sensor), 45.5})
			}
		}
		encoded, err := MarshalMetricHistory(context.Background(), samples)
		if sensors == 6 && (err != nil || len(encoded) > MaxMetricHistoryResponseBytes) {
			t.Fatalf("normal 7-day six-sensor bytes=%d error=%v", len(encoded), err)
		}
		if sensors == 6 && err == nil {
			t.Logf("seven-day six-sensor fixture: %d parents, %d temperatures, %d encoded bytes", len(samples), len(samples)*sensors, len(encoded))
		}
		if sensors == 7 && !errors.Is(err, ErrMetricHistoryLimitExceeded) {
			t.Fatalf("seven-sensor error=%v", err)
		}
	}
}

func TestMetricsHistoryInvalidLegacyData(t *testing.T) {
	at := time.Date(2026, 10, 13, 1, 2, 0, 0, time.UTC)
	for _, statement := range []string{
		`UPDATE metric_samples SET cpu_percent = 'private-invalid-value'`,
		`UPDATE metric_samples SET cpu_percent = 1e999`,
		`UPDATE metric_samples SET cpu_percent = zeroblob(8388609)`,
		`UPDATE metric_samples SET network_receive_bytes = -1`,
		`INSERT INTO metric_temperature_samples VALUES (?, CAST(x'ff' AS TEXT), 20)`,
		`INSERT INTO metric_temperature_samples VALUES (?, 'bad-float', 1e999)`,
		`INSERT INTO metric_temperature_samples VALUES (?, 'bad-type', zeroblob(8388609))`,
	} {
		t.Run(statement, func(t *testing.T) {
			s := metricsTestStore(t)
			historyInsertParent(t, s, at)
			var args []any
			if strings.Contains(statement, "?") {
				args = []any{at.Unix()}
			}
			if _, err := s.database.Exec(statement, args...); err != nil {
				t.Fatal(err)
			}
			if got, err := s.MetricSamplesInRange(context.Background(), at, at); !errors.Is(err, ErrInvalidMetricHistory) || got != nil {
				t.Fatalf("invalid legacy result=%d error=%v", len(got), err)
			}
			if metricsCount(t, s, "metric_samples") != 1 {
				t.Fatal("invalid legacy parent removed")
			}
		})
	}
}

func TestMetricsHistoryCancellationDuringSQLAndRecovery(t *testing.T) {
	s := metricsTestStore(t)
	at := time.Date(2026, 10, 13, 1, 2, 0, 0, time.UTC)
	historyInsertParent(t, s, at)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.MetricSamplesInRange(ctx, at, at); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read error=%v", err)
	}
	// 临时 SQLite 的昂贵递归视图只用于证明正在执行的 SQL 会被取消。
	if _, err := s.database.Exec(`ALTER TABLE metric_temperature_samples RENAME TO saved_temperatures`); err != nil {
		t.Fatal(err)
	}
	view := fmt.Sprintf(`CREATE VIEW metric_temperature_samples AS WITH RECURSIVE n(v) AS
 (SELECT 1 UNION ALL SELECT v+1 FROM n WHERE v < 100000000)
 SELECT %d AS collected_at_unix, printf('sensor-%%09d', v) AS sensor_name, 20 AS temperature_celsius FROM n`, at.Unix())
	if _, err := s.database.Exec(view); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if got, err := s.MetricSamplesInRange(ctx, at, at); !errors.Is(err, context.DeadlineExceeded) || got != nil {
		t.Fatalf("running SQL cancellation result=%d error=%v", len(got), err)
	}
	if _, err := s.database.Exec(`DROP VIEW metric_temperature_samples; ALTER TABLE saved_temperatures RENAME TO metric_temperature_samples`); err != nil {
		t.Fatal(err)
	}
	ctx, cancelRecovery := context.WithTimeout(context.Background(), time.Second)
	defer cancelRecovery()
	if got, err := s.MetricSamplesInRange(ctx, at, at); err != nil || len(got) != 1 {
		t.Fatalf("single connection not released: result=%d error=%v", len(got), err)
	}
}

var historyHookDriverSequence atomic.Int64

type historyHookDriver struct {
	driver.Driver
	beforeQuery func(string) error
}

func (d historyHookDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return historyHookConn{Conn: conn, beforeQuery: d.beforeQuery}, nil
}

type historyHookConn struct {
	driver.Conn
	beforeQuery func(string) error
}

func (c historyHookConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, options)
}

func (c historyHookConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.beforeQuery(query); err != nil {
		return nil, err
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func TestMetricsHistoryUsesOneConsistentReadTransaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.sqlite")
	writer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.database.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 13, 1, 2, 0, 0, time.UTC)
	if err := writer.RecordMetricSample(context.Background(), MetricSample{CollectedAt: at, CPUPercent: 41, Temperatures: []MetricTemperature{{"cpu", 41}}}); err != nil {
		t.Fatal(err)
	}
	changed := false
	driverName := fmt.Sprintf("history-snapshot-%d", historyHookDriverSequence.Add(1))
	sql.Register(driverName, historyHookDriver{Driver: &sqlite.Driver{}, beforeQuery: func(query string) error {
		if !strings.Contains(query, "WITH selected_parents") || changed {
			return nil
		}
		changed = true
		// 精确插在父查询结束与子查询开始之间提交另一个连接的更新。
		tx, err := writer.database.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`UPDATE metric_samples SET cpu_percent=99`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE metric_temperature_samples SET temperature_celsius=99`); err != nil {
			return err
		}
		return tx.Commit()
	}})
	database, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	reader := &Store{database: database}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := reader.MetricSamplesInRange(ctx, at, at)
	if err != nil || !changed || len(got) != 1 || len(got[0].Temperatures) != 1 || got[0].CPUPercent != 41 || got[0].Temperatures[0].TemperatureCelsius != 41 {
		t.Fatalf("mixed snapshot=%+v changed=%v error=%v", got, changed, err)
	}
	got, err = reader.MetricSamplesInRange(ctx, at, at)
	if err != nil || got[0].CPUPercent != 99 || got[0].Temperatures[0].TemperatureCelsius != 99 {
		t.Fatalf("next snapshot=%+v error=%v", got, err)
	}
}
