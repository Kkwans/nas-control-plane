package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
)

type connectHTTPAgent struct {
	DatabaseAgentClient
	discoverCalls int
	testCalls     int
	discoverErr   error
	diagnostic    ncpdatabase.ConnectionDiagnostic
	err           error
}

func (a *connectHTTPAgent) DiscoverDatabases(context.Context, string) (ncpdatabase.Discovery, error) {
	a.discoverCalls++
	return ncpdatabase.Discovery{Sources: []ncpdatabase.Source{{ID: "fixture", Driver: ncpdatabase.DriverPostgreSQL, Host: "synthetic.invalid", Port: 5432, DefaultDatabase: "fixture"}}}, a.discoverErr
}

func (a *connectHTTPAgent) TestDatabaseConnection(context.Context, string, ncpdatabase.Connection) (ncpdatabase.ConnectionDiagnostic, error) {
	a.testCalls++
	return a.diagnostic, a.err
}

type connectHTTPCoordinator struct {
	calls       int
	source      ncpdatabase.Source
	credentials *ncpdatabase.Credentials
	diagnostic  ncpdatabase.ConnectionDiagnostic
	err         error
}

func (c *connectHTTPCoordinator) Connect(_ context.Context, source ncpdatabase.Source, credentials *ncpdatabase.Credentials) (ncpdatabase.ConnectionDiagnostic, error) {
	c.calls++
	c.source, c.credentials = source, credentials
	return c.diagnostic, c.err
}

func databaseConnectRecorder(t *testing.T, api *handler, body string) (*httptest.ResponseRecorder, *requestMetadata) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/connect", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	metadata := &requestMetadata{}
	ctx := context.WithValue(request.Context(), requestIDContextKey{}, "connect-fixture")
	request = request.WithContext(context.WithValue(ctx, requestMetadataContextKey{}, metadata))
	response := httptest.NewRecorder()
	api.databaseConnect(response, request)
	return response, metadata
}

func assertCredentialConflictHTTP(t *testing.T, response *httptest.ResponseRecorder, metadata *requestMetadata) {
	t.Helper()
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	var envelope map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"code":      "credential_conflict",
		"message":   "凭据已被其他请求更改，本次凭据未保存，请重新确认后手动重试。",
		"requestId": "connect-fixture",
	}
	if !reflect.DeepEqual(envelope, want) || metadata.errorCode != "credential_conflict" {
		t.Fatalf("unexpected conflict response or metadata: %v, %v", envelope, metadata)
	}
}

// 直接调用处理器和 Recorder，不创建监听器，也不执行真实数据库连接。
func TestDatabaseConnectHTTPConflictPreservesEnvelopeWithoutReplay(t *testing.T) {
	for _, operation := range []string{"manual_persist", "manual_connect", "manual_snapshot"} {
		t.Run(operation, func(t *testing.T) {
			agent := &connectHTTPAgent{}
			coordinator := &connectHTTPCoordinator{
				diagnostic: ncpdatabase.ConnectionDiagnostic{
					Connected: operation == "manual_persist", Code: "credential_conflict", Operation: operation,
					Endpoint: "postgres://password=private@host/database", Database: "private-database",
				},
				err: fmt.Errorf("token=private: %w", &ncpdatabase.DatabaseError{
					Code: ncpdatabase.CodeCredentialConflict, Operation: operation, Cause: errors.New("password=private"),
				}),
			}
			api := &handler{databaseAgent: agent, databaseConnections: coordinator}
			response, metadata := databaseConnectRecorder(t, api, `{"sourceId":"fixture","credentials":{"username":"root","password":"private-password"}}`)
			assertCredentialConflictHTTP(t, response, metadata)
			if agent.discoverCalls != 1 || agent.testCalls != 0 || coordinator.calls != 1 {
				t.Fatalf("connect replayed: discover/test/connect = %d/%d/%d", agent.discoverCalls, agent.testCalls, coordinator.calls)
			}
			if coordinator.source.ID != "fixture" || coordinator.credentials == nil || coordinator.credentials.Password != "private-password" {
				t.Fatal("manual connection input changed")
			}
		})
	}
}

func TestDatabaseConnectHTTPAgentConflictDoesNotClaimTestSuccess(t *testing.T) {
	agent := &connectHTTPAgent{
		err: &ncpdatabase.DatabaseError{Code: ncpdatabase.CodeCredentialConflict, Cause: errors.New("private Agent detail")},
	}
	response, metadata := databaseConnectRecorder(t, &handler{databaseAgent: agent}, `{"sourceId":"fixture","credentials":{"username":"root"}}`)
	assertCredentialConflictHTTP(t, response, metadata)
	if agent.discoverCalls != 1 || agent.testCalls != 1 {
		t.Fatalf("Agent calls = discover %d, test %d", agent.discoverCalls, agent.testCalls)
	}
}

func TestDatabaseConnectHTTPDiscoveryConflictDoesNotTest(t *testing.T) {
	agent := &connectHTTPAgent{discoverErr: &ncpdatabase.DatabaseError{Code: ncpdatabase.CodeCredentialConflict}}
	coordinator := &connectHTTPCoordinator{}
	response, metadata := databaseConnectRecorder(t, &handler{databaseAgent: agent, databaseConnections: coordinator}, `{"sourceId":"fixture"}`)
	assertCredentialConflictHTTP(t, response, metadata)
	if agent.discoverCalls != 1 || agent.testCalls != 0 || coordinator.calls != 0 {
		t.Fatalf("discovery failure continued: discover/test/connect = %d/%d/%d", agent.discoverCalls, agent.testCalls, coordinator.calls)
	}
}

func TestDatabaseConnectHTTPPreservesOtherErrors(t *testing.T) {
	for _, testCase := range []struct {
		code   ncpdatabase.ErrorCode
		status int
	}{
		{ncpdatabase.CodeAuthFailed, http.StatusUnauthorized},
		{ncpdatabase.CodeTimeout, http.StatusGatewayTimeout},
		{ncpdatabase.CodeCredentialStoreUnavailable, http.StatusServiceUnavailable},
		{ncpdatabase.CodeConstraintFailed, http.StatusConflict},
	} {
		t.Run(string(testCase.code), func(t *testing.T) {
			agent := &connectHTTPAgent{}
			coordinator := &connectHTTPCoordinator{
				diagnostic: ncpdatabase.ConnectionDiagnostic{Connected: true, Operation: "manual_persist"},
				err:        &ncpdatabase.DatabaseError{Code: testCase.code, Cause: errors.New("private details")},
			}
			response, _ := databaseConnectRecorder(t, &handler{databaseAgent: agent, databaseConnections: coordinator}, `{"sourceId":"fixture","credentials":{}}`)
			var envelope ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if response.Code != testCase.status || envelope.Code != string(testCase.code) || strings.Contains(envelope.Message, "未保存") || strings.Contains(response.Body.String(), "private") {
				t.Fatalf("other error gained a conflict claim: %d %s", response.Code, response.Body)
			}
			if coordinator.calls != 1 || agent.testCalls != 0 {
				t.Fatal("failed connection was replayed")
			}
		})
	}
}

func TestDatabaseCredentialConflictGenericHTTPDoesNotClaimConnectOutcome(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/query", nil)
	(&handler{}).writeDatabaseError(response, request, &ncpdatabase.DatabaseError{Code: ncpdatabase.CodeCredentialConflict})
	var envelope ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusConflict || envelope.Code != "credential_conflict" || strings.Contains(envelope.Message, "未保存") || strings.Contains(envelope.Message, "成功") {
		t.Fatalf("generic conflict claimed connect outcome: %d %s", response.Code, response.Body)
	}
}

type connectHTTPTester func(context.Context, ncpdatabase.Connection) (ncpdatabase.ConnectionDiagnostic, error)

func (f connectHTTPTester) TestConnection(ctx context.Context, connection ncpdatabase.Connection) (ncpdatabase.ConnectionDiagnostic, error) {
	return f(ctx, connection)
}

type connectHTTPStore struct {
	ncpdatabase.CredentialStore
	reads, writes, committed int
}

func (s *connectHTTPStore) ReadCredentialSnapshot(ctx context.Context, sourceID string) (ncpdatabase.CredentialSnapshot, error) {
	s.reads++
	return s.CredentialStore.ReadCredentialSnapshot(ctx, sourceID)
}

func (s *connectHTTPStore) CompareAndSwapCredential(ctx context.Context, baseline ncpdatabase.CredentialBaseline, record ncpdatabase.StoredCredential) error {
	s.writes++
	err := s.CredentialStore.CompareAndSwapCredential(ctx, baseline, record)
	if err == nil {
		s.committed++
	}
	return err
}

func TestDatabaseConnectHTTPConcurrentSavePreservesWinner(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing=%t", existing), func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "credentials.db")
			openStore := func() *ncpdatabase.SQLiteCredentialStore {
				store, err := ncpdatabase.OpenSQLiteCredentialStore(path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				return store
			}
			lateStore, otherStore := openStore(), openStore()
			keys, err := ncpdatabase.NewMemoryKeyProvider(1, bytes.Repeat([]byte{0x53}, 32))
			if err != nil {
				t.Fatal(err)
			}
			newVault := func(store ncpdatabase.CredentialStore) *ncpdatabase.CredentialVault {
				vault, err := ncpdatabase.NewCredentialVault(store, keys)
				if err != nil {
					t.Fatal(err)
				}
				return vault
			}
			writerVault := newVault(otherStore)
			writer, err := ncpdatabase.NewConnectionCoordinator(writerVault, connectHTTPTester(func(context.Context, ncpdatabase.Connection) (ncpdatabase.ConnectionDiagnostic, error) {
				return ncpdatabase.ConnectionDiagnostic{Connected: true, Code: "connected"}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			source := ncpdatabase.Source{ID: "fixture", Driver: ncpdatabase.DriverPostgreSQL, Host: "synthetic.invalid", Port: 5432, DefaultDatabase: "fixture"}
			if existing {
				if _, err := writer.ManualConnect(ctx, source, ncpdatabase.Credentials{Username: "fixture-user", Password: "synthetic-initial"}); err != nil {
					t.Fatal(err)
				}
			}
			counted := &connectHTTPStore{CredentialStore: lateStore}
			started, release := make(chan struct{}), make(chan struct{})
			tests := 0
			coordinator, err := ncpdatabase.NewConnectionCoordinator(newVault(counted), connectHTTPTester(func(ctx context.Context, connection ncpdatabase.Connection) (ncpdatabase.ConnectionDiagnostic, error) {
				tests++
				if tests == 1 {
					close(started)
				}
				select {
				case <-release:
					return ncpdatabase.ConnectionDiagnostic{Connected: true, Code: "connected"}, nil
				case <-ctx.Done():
					return ncpdatabase.ConnectionDiagnostic{}, ctx.Err()
				}
			}))
			if err != nil {
				t.Fatal(err)
			}
			agent := &connectHTTPAgent{}
			api := &handler{databaseAgent: agent, databaseConnections: coordinator}
			type outcome struct {
				response *httptest.ResponseRecorder
				metadata *requestMetadata
			}
			done := make(chan outcome, 1)
			go func() {
				response, metadata := databaseConnectRecorder(t, api, `{"sourceId":"fixture","credentials":{"username":"fixture-user","password":"synthetic-late"}}`)
				done <- outcome{response, metadata}
			}()
			select {
			case <-started:
			case result := <-done:
				t.Fatalf("connection ended before test: %d %s", result.response.Code, result.response.Body)
			}
			// 两个真实协调器在独立 SQLite 连接上交错；通道固定胜方先保存。
			winnerCredentials := ncpdatabase.Credentials{Username: "fixture-user", Password: "synthetic-winner"}
			_, saveErr := writer.ManualConnect(ctx, source, winnerCredentials)
			winner, snapshotErr := otherStore.ReadCredentialSnapshot(ctx, source.ID)
			close(release)
			result := <-done
			if saveErr != nil || snapshotErr != nil {
				t.Fatalf("competing save/snapshot failed: %v / %v", saveErr, snapshotErr)
			}
			assertCredentialConflictHTTP(t, result.response, result.metadata)
			final, err := lateStore.ReadCredentialSnapshot(ctx, source.ID)
			if err != nil || !reflect.DeepEqual(final, winner) || !final.Baseline.Exists || final.Baseline.Revision <= 0 {
				t.Fatalf("HTTP 409 changed the winning credential row or revision: %v", err)
			}
			resolved, found, err := writerVault.Resolve(ctx, source)
			if err != nil || !found || resolved != winnerCredentials {
				t.Fatalf("winning credentials were not retained: %v", err)
			}
			if tests != 1 || counted.reads != 1 || counted.writes != 1 || counted.committed != 0 || agent.discoverCalls != 1 || agent.testCalls != 0 {
				t.Fatalf("late attempt replayed or wrote: tests=%d reads=%d writes=%d committed=%d discover=%d Agent tests=%d", tests, counted.reads, counted.writes, counted.committed, agent.discoverCalls, agent.testCalls)
			}
		})
	}
}
