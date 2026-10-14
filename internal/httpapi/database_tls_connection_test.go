package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
)

type tlsHTTPProtectedCoordinator struct {
	connectHTTPCoordinator
	tests, resolves int
	testSource      ncpdatabase.Source
	testCredentials ncpdatabase.Credentials
	manual          *ncpdatabase.Credentials
	err             error
	mutatePolicy    bool
}

func (c *tlsHTTPProtectedCoordinator) TestConnection(_ context.Context, source ncpdatabase.Source, credentials ncpdatabase.Credentials) (ncpdatabase.ConnectionDiagnostic, error) {
	c.tests++
	c.testSource, c.testCredentials = source, credentials
	return ncpdatabase.ConnectionDiagnostic{Connected: true, Code: "connected"}, c.err
}

func (c *tlsHTTPProtectedCoordinator) ResolveConnection(_ context.Context, source ncpdatabase.Source, manual *ncpdatabase.Credentials) (ncpdatabase.Connection, error) {
	c.resolves++
	c.manual = manual
	credentials := ncpdatabase.Credentials{}
	if manual != nil {
		credentials = *manual
		if c.mutatePolicy && credentials.TLS != nil {
			credentials.TLS.ServerName = "resolver-copy.invalid"
		}
	}
	return ncpdatabase.Connection{SourceID: source.ID, Credentials: credentials}, c.err
}

func TestDatabaseTLSHTTPConfiguredTestUsesProtectedExtension(t *testing.T) {
	agent := &tlsHTTPAgent{}
	coordinator := &tlsHTTPProtectedCoordinator{}
	api := &handler{databaseAgent: agent, databaseConnections: coordinator}
	response := tlsHTTPRecorder(api, tlsHTTPEndpoints()[1], `{"sourceId":"fixture","credentials":{"username":"manual-user","password":"synthetic-password"}}`)
	if response.Code != http.StatusOK || coordinator.tests != 1 || coordinator.calls != 0 || coordinator.resolves != 0 || agent.testCalls != 0 || agent.discoverCalls != 1 {
		t.Fatalf("protected diagnostic routing changed: %d %s", response.Code, response.Body)
	}
	if coordinator.testSource.Host != "synthetic.invalid" || coordinator.testCredentials.Username != "manual-user" || coordinator.testCredentials.Password != "synthetic-password" {
		t.Fatal("protected diagnostic did not receive discovered source and manual credentials")
	}
}

func TestDatabaseTLSHTTPConfiguredMissingExtensionsFailClosed(t *testing.T) {
	for _, endpoint := range tlsHTTPEndpoints()[1:] {
		t.Run(endpoint.path, func(t *testing.T) {
			agent := &tlsHTTPAgent{}
			coordinator := &connectHTTPCoordinator{}
			api := &handler{databaseAgent: agent, databaseConnections: coordinator}
			response := tlsHTTPRecorder(api, endpoint, `{"sourceId":"fixture"`+endpoint.extra+`}`)
			if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"credential_store_unavailable"`) {
				t.Fatalf("missing extension did not fail closed: %d %s", response.Code, response.Body)
			}
			if coordinator.calls != 0 || agent.discoverCalls != 0 || agent.testCalls != 0 || agent.operationCalls != 0 {
				t.Fatal("missing protected capability reached a fallback")
			}
		})
	}
}

func TestDatabaseTLSHTTPTypedNilCoordinatorIsNotLegacyFallback(t *testing.T) {
	var coordinator *ncpdatabase.ConnectionCoordinator
	for _, endpoint := range tlsHTTPEndpoints()[1:] {
		t.Run(endpoint.path, func(t *testing.T) {
			agent := &tlsHTTPAgent{}
			api := &handler{databaseAgent: agent, databaseConnections: coordinator}
			response := tlsHTTPRecorder(api, endpoint, `{"sourceId":"fixture"`+endpoint.extra+`}`)
			if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"credential_store_unavailable"`) {
				t.Fatalf("typed nil coordinator did not fail closed: %d %s", response.Code, response.Body)
			}
			if agent.testCalls != 0 || agent.operationCalls != 0 {
				t.Fatal("typed nil coordinator used legacy direct Agent path")
			}
		})
	}
}

func TestDatabaseTLSHTTPProtectedTestStopsAfterDiscoveryOrPreparationFailure(t *testing.T) {
	for _, testCase := range []struct {
		name, sourceID string
		discoverErr    error
		prepareErr     error
		status         int
		tests          int
	}{
		{"missing source", "unknown", nil, nil, http.StatusNotFound, 0},
		{"discovery failed", "fixture", &ncpdatabase.DatabaseError{Code: ncpdatabase.CodeAgentUnavailable}, nil, http.StatusServiceUnavailable, 0},
		{"saved policy failed", "fixture", nil, &ncpdatabase.DatabaseError{Code: ncpdatabase.CodeCredentialCorrupt}, http.StatusServiceUnavailable, 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			agent := &tlsHTTPAgent{connectHTTPAgent: connectHTTPAgent{discoverErr: testCase.discoverErr}}
			coordinator := &tlsHTTPProtectedCoordinator{err: testCase.prepareErr}
			api := &handler{databaseAgent: agent, databaseConnections: coordinator}
			response := tlsHTTPRecorder(api, tlsHTTPEndpoints()[1], `{"sourceId":"`+testCase.sourceID+`"}`)
			if response.Code != testCase.status || agent.discoverCalls != 1 || agent.testCalls != 0 || coordinator.tests != testCase.tests || coordinator.calls != 0 {
				t.Fatalf("failed preparation continued or replayed: %d %s", response.Code, response.Body)
			}
		})
	}
}

func TestDatabaseTLSHTTPOnlyPolicyIsManualAndDeepCopied(t *testing.T) {
	agent := &tlsHTTPAgent{}
	coordinator := &tlsHTTPProtectedCoordinator{mutatePolicy: true}
	api := &handler{databaseAgent: agent, databaseConnections: coordinator}
	credentials := ncpdatabase.Credentials{TLS: &ncpdatabase.TLSPolicy{Mode: ncpdatabase.TLSVerifyFull, ServerName: "original.invalid"}}
	if !hasDatabaseCredentials(credentials) || hasDatabaseCredentials(ncpdatabase.Credentials{}) {
		t.Fatal("TLS-only credentials must be manual")
	}
	_, err := api.prepareDatabaseConnection(context.Background(), ncpdatabase.Connection{SourceID: "fixture", Credentials: credentials})
	if err != nil || coordinator.resolves != 1 || coordinator.manual == nil || coordinator.manual.Username != "" || coordinator.manual.Password != "" {
		t.Fatalf("TLS-only request inherited an automatic credential: %v", err)
	}
	if credentials.TLS.ServerName != "original.invalid" || coordinator.manual.TLS == credentials.TLS {
		t.Fatal("HTTP preparation shared caller-owned TLS policy")
	}
}

func TestDatabaseTLSHTTPVisibleErrorsStaySafeAndDoNotClaimOutcome(t *testing.T) {
	for _, code := range []ncpdatabase.ErrorCode{ncpdatabase.CodeTLSConfigurationInvalid, ncpdatabase.CodeTLSPolicyUnsupported, ncpdatabase.CodeTLSVerificationFailed} {
		for _, connect := range []bool{false, true} {
			t.Run(string(code)+map[bool]string{false: "/query", true: "/connect"}[connect], func(t *testing.T) {
				response := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/query", nil)
				err := &ncpdatabase.DatabaseError{Code: code, Cause: errors.New("certificate=private; password=synthetic-secret; host=private")}
				if connect {
					(&handler{}).writeDatabaseConnectError(response, request, err)
				} else {
					(&handler{}).writeDatabaseError(response, request, err)
				}
				var envelope ErrorResponse
				if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if response.Code != http.StatusBadRequest || envelope.Code != string(code) {
					t.Fatalf("TLS error lost code/status: %d %s", response.Code, response.Body)
				}
				for _, forbidden := range []string{"private", "synthetic-secret", "未保存", "未执行", "成功"} {
					if strings.Contains(envelope.Message, forbidden) {
						t.Fatalf("TLS response leaked detail or promised outcome: %s", response.Body)
					}
				}
			})
		}
	}
}

func TestDatabaseTLSHTTPTestOnlyInheritsSavedPolicyWithoutWriting(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "enabled", true: "disabled"}[disabled], func(t *testing.T) {
			ctx := context.Background()
			store, err := ncpdatabase.OpenSQLiteCredentialStore(filepath.Join(t.TempDir(), "credentials.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			counted := &connectHTTPStore{CredentialStore: store}
			keys, err := ncpdatabase.NewMemoryKeyProvider(1, bytes.Repeat([]byte{0x6b}, 32))
			if err != nil {
				t.Fatal(err)
			}
			vault, err := ncpdatabase.NewCredentialVault(counted, keys)
			if err != nil {
				t.Fatal(err)
			}
			var observed ncpdatabase.Connection
			tests := 0
			coordinator, err := ncpdatabase.NewConnectionCoordinator(vault, connectHTTPTester(func(_ context.Context, connection ncpdatabase.Connection) (ncpdatabase.ConnectionDiagnostic, error) {
				tests++
				observed = ncpdatabase.Connection{SourceID: connection.SourceID, Credentials: ncpdatabase.CloneCredentials(connection.Credentials)}
				return ncpdatabase.ConnectionDiagnostic{Connected: true, Code: "connected"}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			source := ncpdatabase.Source{ID: "fixture", Driver: ncpdatabase.DriverPostgreSQL, Host: "synthetic.invalid", Port: 5432, DefaultDatabase: "fixture"}
			policy := &ncpdatabase.TLSPolicy{Mode: ncpdatabase.TLSVerifyFull, ServerName: "synthetic.invalid"}
			if _, err := coordinator.ManualConnect(ctx, source, ncpdatabase.Credentials{Username: "saved-user", Password: "synthetic-saved", TLS: policy}); err != nil {
				t.Fatal(err)
			}
			if disabled {
				if err := vault.DisableAutomatic(ctx, source.ID, ncpdatabase.CodeAuthFailed); err != nil {
					t.Fatal(err)
				}
			}
			before, err := store.ReadCredentialSnapshot(ctx, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			tests, counted.writes, counted.committed = 0, 0, 0
			agent := &tlsHTTPAgent{}
			api := &handler{databaseAgent: agent, databaseConnections: coordinator}
			response := tlsHTTPRecorder(api, tlsHTTPEndpoints()[1], `{"sourceId":"fixture","credentials":{"username":"fresh-user","password":"synthetic-fresh"}}`)
			if response.Code != http.StatusOK || tests != 1 || agent.discoverCalls != 1 || agent.testCalls != 0 || counted.writes != 0 || counted.committed != 0 {
				t.Fatalf("test-only wrote/replayed/bypassed: %d %s", response.Code, response.Body)
			}
			if observed.Credentials.Username != "fresh-user" || observed.Credentials.Password != "synthetic-fresh" || !reflect.DeepEqual(observed.Credentials.TLS, policy) {
				t.Fatal("test-only lost saved TLS policy or replaced manual identity")
			}
			after, err := store.ReadCredentialSnapshot(ctx, source.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("test-only modified saved record or revision: %v", err)
			}
			for _, endpoint := range tlsHTTPEndpoints()[2:] {
				response := tlsHTTPRecorder(api, endpoint, `{"sourceId":"fixture","credentials":{"username":"fresh-user","password":"synthetic-fresh"}`+endpoint.extra+`}`)
				if response.Code != http.StatusOK && response.Code != http.StatusCreated {
					t.Fatalf("protected %s rejected manual inherited policy: %d %s", endpoint.path, response.Code, response.Body)
				}
				if !reflect.DeepEqual(agent.connection.Credentials.TLS, policy) || agent.connection.Credentials.Password != "synthetic-fresh" {
					t.Fatalf("protected %s lost policy or manual credentials", endpoint.path)
				}
			}
			manualFinal, err := store.ReadCredentialSnapshot(ctx, source.ID)
			if err != nil || !reflect.DeepEqual(before, manualFinal) || counted.writes != 0 {
				t.Fatalf("manual resolution modified saved credentials: %v", err)
			}
			for _, endpoint := range tlsHTTPEndpoints()[2:] {
				calls := agent.operationCalls
				response := tlsHTTPRecorder(api, endpoint, `{"sourceId":"fixture"`+endpoint.extra+`}`)
				if disabled {
					if response.Code != http.StatusBadRequest || agent.operationCalls != calls || !strings.Contains(response.Body.String(), `"code":"credentials_required"`) {
						t.Fatalf("disabled saved credential reached %s: %d %s", endpoint.path, response.Code, response.Body)
					}
				} else {
					if (response.Code != http.StatusOK && response.Code != http.StatusCreated) || agent.operationCalls != calls+1 || !reflect.DeepEqual(agent.connection.Credentials.TLS, policy) || agent.connection.Credentials.Password != "synthetic-saved" {
						t.Fatalf("automatic %s lost saved policy/identity: %d %s", endpoint.path, response.Code, response.Body)
					}
				}
			}
			final, err := store.ReadCredentialSnapshot(ctx, source.ID)
			if err != nil || !bytes.Equal(before.Record.Ciphertext, final.Record.Ciphertext) || before.Record.Username != final.Record.Username || before.Record.AutomaticEnabled != final.Record.AutomaticEnabled || counted.writes != 0 {
				t.Fatalf("automatic resolution replaced saved credentials: %v", err)
			}
		})
	}
}
