package database

import (
	"context"
	"testing"
)

func TestManagerByteLimitedReturningMayStillWriteAllValues(t *testing.T) {
	manager, connection, db := newQueryFixture(t)
	if _, err := db.Exec(`INSERT INTO sample VALUES(1,0,'old')`); err != nil {
		t.Fatal(err)
	}
	result := runFixtureQuery(t, manager, connection, `UPDATE sample SET n=n+1,label=zeroblob(2097152) RETURNING id,label`, QueryResultModeRows)
	if len(result.Columns) != 2 || len(result.Rows) != 0 || !result.Truncated || result.TruncationReason != "byte_limit" || result.RowsAffectedKnown || result.RowsAffected != 0 {
		t.Fatalf("empty retained prefix=%+v", result)
	}
	assertFixtureScalar(t, db, `SELECT n FROM sample WHERE id=1`, 1)
	assertFixtureScalar(t, db, `SELECT length(label) FROM sample WHERE id=1`, 2097152)
}

func TestManagerPagedRowsByteLimitDoesNotShortenPage(t *testing.T) {
	manager, connection, db := newQueryFixture(t)
	if _, err := db.Exec(`INSERT INTO sample VALUES(1,0,'small'),(2,0,zeroblob(2097152)),(3,0,'last')`); err != nil {
		t.Fatal(err)
	}
	request := RowsRequest{Connection: connection, Table: "sample", Limit: 1}
	result, err := manager.Rows(context.Background(), request)
	if err != nil || len(result.Rows) != 1 || result.Rows[0]["id"] != int64(1) || !result.HasMore || result.Offset != 0 || result.Limit != 1 {
		t.Fatalf("first page=%+v error=%v", result, err)
	}
	request.Limit = 2
	result, err = manager.Rows(context.Background(), request)
	if ErrorCodeOf(err) != CodeResultTooLarge || result.Rows != nil {
		t.Fatalf("whole page must fail: result=%+v error=%v", result, err)
	}
	request.Limit, request.Offset = 1, 1
	result, err = manager.Rows(context.Background(), request)
	if ErrorCodeOf(err) != CodeResultTooLarge || result.Rows != nil {
		t.Fatalf("oversized next page=%+v error=%v", result, err)
	}
	request.Offset = 2
	result, err = manager.Rows(context.Background(), request)
	if err != nil || len(result.Rows) != 1 || result.Rows[0]["id"] != int64(3) || result.HasMore {
		t.Fatalf("explicit offset=%+v error=%v", result, err)
	}
}
