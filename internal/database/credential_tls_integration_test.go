package database

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// 本文件只使用合成凭据、临时 SQLite 和纯内存 tester，不进行网络连接。
func integratedTLSCredentials(label string) Credentials {
	credentials := activationCredentials(label)
	credentials.TLS = &TLSPolicy{Mode: TLSVerifyFull, ServerName: "identity.invalid"}
	return credentials
}

func integratedTLSCoordinator(t *testing.T, vault *CredentialVault, test activationTester) *ConnectionCoordinator {
	t.Helper()
	coordinator, err := NewConnectionCoordinator(vault, test)
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

func assertIntegratedTLSRecord(t *testing.T, store CredentialStore, keys KeyProvider, source Source, want Credentials) StoredCredential {
	t.Helper()
	record, err := store.GetCredential(context.Background(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecryptCredentials(context.Background(), keys, credentialAAD(record.SourceID, record.Driver, record.Endpoint), record.Ciphertext)
	if err != nil || !reflect.DeepEqual(got, want) || got.TLS == want.TLS {
		t.Fatalf("saved v2 credentials or policy changed: %v", err)
	}
	if versionedEnvelope(t, record.Ciphertext).Version != 2 {
		t.Fatal("strict credential was downgraded")
	}
	return record
}

func TestCredentialTLSManualAndTestOnlyInheritWithoutBorrowingSecrets(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		for _, operation := range []string{"manual", "test-only", "resolver"} {
			t.Run(operation+map[bool]string{false: "/enabled", true: "/disabled"}[disabled], func(t *testing.T) {
				store, keys := revisionTestStore(t), activationKeys(t)
				vault, source := activationVault(t, store, keys), activationSource("source")
				saved := integratedTLSCredentials("saved")
				activationSave(t, vault, source, saved)
				if disabled {
					if err := vault.DisableAutomatic(context.Background(), source.ID, CodeAuthFailed); err != nil {
						t.Fatal(err)
					}
				}
				before := revisionSnapshot(t, store, source.ID)
				manual := Credentials{Username: "new-user", Database: "another-database"}
				want := CloneCredentials(manual)
				want.TLS = CloneCredentials(saved).TLS
				tests := 0
				coordinator := integratedTLSCoordinator(t, vault, func(_ context.Context, request Connection) (ConnectionDiagnostic, error) {
					tests++
					if !reflect.DeepEqual(request.Credentials, want) || request.Credentials.TLS == saved.TLS {
						t.Fatal("did not independently inherit policy, or borrowed saved authentication")
					}
					request.Credentials.TLS.ServerName = "tester-mutated.invalid"
					return ConnectionDiagnostic{Connected: true}, nil
				})
				switch operation {
				case "manual":
					if _, err := coordinator.ManualConnect(context.Background(), source, manual); err != nil {
						t.Fatal(err)
					}
					after := assertIntegratedTLSRecord(t, store, keys, source, want)
					if !after.AutomaticEnabled || after.Revision <= before.Baseline.Revision {
						t.Fatal("successful manual save did not advance the original snapshot")
					}
				case "test-only":
					if _, err := coordinator.TestConnection(context.Background(), source, manual); err != nil {
						t.Fatal(err)
					}
					revisionAssertUnchanged(t, store, before)
				case "resolver":
					resolved, err := coordinator.ResolveConnection(context.Background(), source, &manual)
					if err != nil || !reflect.DeepEqual(resolved.Credentials, want) {
						t.Fatalf("manual resolver lost inherited policy: %v", err)
					}
					resolved.Credentials.TLS.ServerName = "resolver-mutated.invalid"
					revisionAssertUnchanged(t, store, before)
				}
				wantTests := 1
				if operation == "resolver" {
					wantTests = 0
				}
				if tests != wantTests || manual.TLS != nil || saved.TLS.ServerName != "identity.invalid" {
					t.Fatal("test count or caller-owned value changed")
				}
			})
		}
	}
}

func TestCredentialTLSOmittedPolicyFailsClosedBeforeTester(t *testing.T) {
	for _, failure := range []string{"corrupt", "version-tamper", "key-unavailable", "host-moved", "port-moved", "location-port-moved", "driver-moved", "disabled-corrupt"} {
		for _, operation := range []string{"manual", "test-only", "resolver"} {
			t.Run(failure+"/"+operation, func(t *testing.T) {
				store, keys := revisionTestStore(t), activationKeys(t)
				source := activationSource("source")
				vault := activationVault(t, store, keys)
				activationSave(t, vault, source, integratedTLSCredentials("saved"))
				var provider KeyProvider = keys
				wantCode := CodeCredentialsRequired
				switch failure {
				case "corrupt", "disabled-corrupt":
					revisionExec(t, store, `UPDATE ncp_database_credentials SET ciphertext = X'01' WHERE source_id = 'source'`)
					wantCode = CodeCredentialCorrupt
					if failure == "disabled-corrupt" {
						if err := vault.DisableAutomatic(context.Background(), source.ID, CodeAuthFailed); err != nil {
							t.Fatal(err)
						}
					}
				case "version-tamper":
					snapshot := revisionSnapshot(t, store, source.ID)
					envelope := versionedEnvelope(t, snapshot.Record.Ciphertext)
					envelope.Version = 1
					snapshot.Record.Ciphertext, _ = json.Marshal(envelope)
					if err := store.CompareAndSwapCredential(context.Background(), snapshot.Baseline, snapshot.Record); err != nil {
						t.Fatal(err)
					}
					wantCode = CodeCredentialCorrupt
				case "key-unavailable":
					provider = &activationKeyProvider{MemoryKeyProvider: keys, lookupError: &DatabaseError{Code: CodeKeyUnavailable}}
					wantCode = CodeKeyUnavailable
				case "host-moved":
					source.Host = "new-target.invalid"
				case "port-moved":
					source.Port++
				case "location-port-moved":
					// Host/Port 不变，实际连接器仍采用 Location 的显式端口。
					source.Location = "ignored.invalid:6543/ignored-database"
				case "driver-moved":
					source.Driver = DriverMySQL
				}
				counted := &activationStore{CredentialStore: store}
				tests := 0
				coordinator := integratedTLSCoordinator(t, activationVault(t, counted, provider), func(context.Context, Connection) (ConnectionDiagnostic, error) {
					tests++
					return ConnectionDiagnostic{Connected: true}, nil
				})
				manual := activationCredentials("new")
				var err error
				switch operation {
				case "manual":
					_, err = coordinator.ManualConnect(context.Background(), source, manual)
				case "test-only":
					_, err = coordinator.TestConnection(context.Background(), source, manual)
				case "resolver":
					_, err = coordinator.ResolveConnection(context.Background(), source, &manual)
				}
				if ErrorCodeOf(err) != wantCode || tests != 0 || counted.writes != 0 || counted.reads != 1 {
					t.Fatalf("unsafe omitted-policy path: err=%v tests=%d reads=%d writes=%d", err, tests, counted.reads, counted.writes)
				}
			})
		}
	}
}

func TestCredentialTLSExplicitStrictPolicyCanRepair(t *testing.T) {
	for _, failure := range []string{"corrupt", "key-unavailable", "host-moved"} {
		t.Run(failure, func(t *testing.T) {
			store, keys := revisionTestStore(t), activationKeys(t)
			source := activationSource("source")
			vault := activationVault(t, store, keys)
			activationSave(t, vault, source, integratedTLSCredentials("old"))
			var provider KeyProvider = keys
			switch failure {
			case "corrupt":
				revisionExec(t, store, `UPDATE ncp_database_credentials SET ciphertext = X'01' WHERE source_id = 'source'`)
			case "key-unavailable":
				provider = &activationKeyProvider{MemoryKeyProvider: keys, lookupError: &DatabaseError{Code: CodeKeyUnavailable}}
			case "host-moved":
				source.Host = "replacement.invalid"
			}
			counted := &activationStore{CredentialStore: store}
			tests := 0
			coordinator := integratedTLSCoordinator(t, activationVault(t, counted, provider), func(_ context.Context, request Connection) (ConnectionDiagnostic, error) {
				tests++
				if request.Credentials.TLS == nil || request.Credentials.Password != "synthetic-new" {
					t.Fatal("explicit repair was changed")
				}
				return ConnectionDiagnostic{Connected: true}, nil
			})
			credentials := integratedTLSCredentials("new")
			credentials.TLS.ServerName = "new-identity.invalid"
			if _, err := coordinator.ManualConnect(context.Background(), source, credentials); err != nil {
				t.Fatal(err)
			}
			assertIntegratedTLSRecord(t, store, keys, source, credentials)
			if tests != 1 || counted.reads != 1 || counted.writes != 1 {
				t.Fatal("repair changed its frozen baseline or retested")
			}
		})
	}
}

func TestCredentialTLSOnlyNeverBorrowsSavedPassword(t *testing.T) {
	store, keys := revisionTestStore(t), activationKeys(t)
	vault, source := activationVault(t, store, keys), activationSource("source")
	activationSave(t, vault, source, integratedTLSCredentials("saved"))
	before := revisionSnapshot(t, store, source.ID)
	coordinator := integratedTLSCoordinator(t, vault, func(context.Context, Connection) (ConnectionDiagnostic, error) {
		t.Fatal("TLS-only input reached tester using saved credentials")
		return ConnectionDiagnostic{}, nil
	})
	input := Credentials{TLS: &TLSPolicy{Mode: TLSVerifyFull}}
	if _, err := coordinator.Connect(context.Background(), source, &input); ErrorCodeOf(err) != CodeCredentialsRequired {
		t.Fatalf("TLS-only must remain manual: %v", err)
	}
	if _, err := coordinator.TestConnection(context.Background(), source, input); ErrorCodeOf(err) != CodeCredentialsRequired {
		t.Fatal(err)
	}
	if _, err := coordinator.ResolveConnection(context.Background(), source, &input); ErrorCodeOf(err) != CodeCredentialsRequired {
		t.Fatal(err)
	}
	revisionAssertUnchanged(t, store, before)
}

func TestCredentialTLSLegacyRetargetAndStrictAutomaticResolve(t *testing.T) {
	store, keys := revisionTestStore(t), activationKeys(t)
	vault, source := activationVault(t, store, keys), activationSource("source")
	activationSave(t, vault, source, activationCredentials("legacy"))
	source.Host = "moved.invalid"
	tests := 0
	coordinator := integratedTLSCoordinator(t, vault, func(_ context.Context, request Connection) (ConnectionDiagnostic, error) {
		tests++
		if request.Credentials.TLS != nil || request.Credentials.Password != "synthetic-manual" {
			t.Fatal("authenticated v1 manual retarget changed credentials")
		}
		return ConnectionDiagnostic{Connected: true}, nil
	})
	if _, err := coordinator.TestConnection(context.Background(), source, activationCredentials("manual")); err != nil || tests != 1 {
		t.Fatalf("authenticated v1 manual retarget blocked: %v", err)
	}
	if _, _, err := vault.Resolve(context.Background(), source); ErrorCodeOf(err) != CodeCredentialsRequired {
		t.Fatalf("automatic v1 mismatch must remain rejected: %v", err)
	}
	strict := integratedTLSCredentials("strict")
	activationSave(t, vault, source, strict)
	for _, resolve := range []func() (Credentials, error){
		func() (Credentials, error) { c, _, err := vault.Resolve(context.Background(), source); return c, err },
		func() (Credentials, error) {
			c, err := coordinator.ResolveConnection(context.Background(), source, nil)
			return c.Credentials, err
		},
	} {
		got, err := resolve()
		if err != nil || !reflect.DeepEqual(got, strict) || got.TLS == strict.TLS {
			t.Fatalf("automatic resolution lost strict policy: %v", err)
		}
		got.TLS.Mode = "disable"
	}
	assertIntegratedTLSRecord(t, store, keys, source, strict)
}

func TestCredentialTLSSQLiteCannotBypassSavedStrictPolicy(t *testing.T) {
	for _, operation := range []string{"vault", "auto", "resolve-auto", "manual", "test-only", "resolve-manual"} {
		t.Run(operation, func(t *testing.T) {
			store, keys := revisionTestStore(t), activationKeys(t)
			vault, source := activationVault(t, store, keys), activationSource("source")
			activationSave(t, vault, source, integratedTLSCredentials("saved"))
			source.Driver, source.Path = DriverSQLite, "/synthetic/path.db"
			coordinator := integratedTLSCoordinator(t, vault, func(context.Context, Connection) (ConnectionDiagnostic, error) {
				t.Fatal("SQLite bypass reached tester")
				return ConnectionDiagnostic{}, nil
			})
			var err error
			switch operation {
			case "vault":
				_, _, err = vault.Resolve(context.Background(), source)
			case "auto":
				_, err = coordinator.AutoConnect(context.Background(), source)
			case "resolve-auto":
				_, err = coordinator.ResolveConnection(context.Background(), source, nil)
			case "manual":
				_, err = coordinator.ManualConnect(context.Background(), source, Credentials{})
			case "test-only":
				_, err = coordinator.TestConnection(context.Background(), source, Credentials{})
			case "resolve-manual":
				_, err = coordinator.ResolveConnection(context.Background(), source, &Credentials{})
			}
			if ErrorCodeOf(err) != CodeCredentialsRequired {
				t.Fatalf("old network policy bypassed or wrong failure: %v", err)
			}
		})
	}
	store, keys := revisionTestStore(t), activationKeys(t)
	vault := activationVault(t, store, keys)
	source := Source{ID: "sqlite", Driver: DriverSQLite, Path: "/synthetic/path.db"}
	tests := 0
	coordinator := integratedTLSCoordinator(t, vault, func(_ context.Context, request Connection) (ConnectionDiagnostic, error) {
		tests++
		if request.Credentials != (Credentials{}) {
			t.Fatal("nil SQLite path acquired credentials")
		}
		return ConnectionDiagnostic{Connected: true}, nil
	})
	if _, err := coordinator.AutoConnect(context.Background(), source); err != nil || tests != 1 {
		t.Fatal("ordinary SQLite path changed")
	}
	if _, err := coordinator.ResolveConnection(context.Background(), source, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.TestConnection(context.Background(), source, Credentials{TLS: &TLSPolicy{Mode: TLSVerifyFull}}); ErrorCodeOf(err) != CodeTLSConfigurationInvalid || tests != 1 {
		t.Fatal("SQLite explicit policy was not rejected before tester")
	}
}

func TestCredentialTLSFreezesSourceAndIndependentTesterCopy(t *testing.T) {
	store, keys := revisionTestStore(t), activationKeys(t)
	counted := &activationStore{CredentialStore: store}
	vault, source := activationVault(t, counted, keys), activationSource("source")
	source.Tags = []string{"original"}
	credentials := integratedTLSCredentials("original")
	want := CloneCredentials(credentials)
	counted.afterRead = func(CredentialSnapshot) {
		credentials.TLS.ServerName = "caller-mutated.invalid"
		source.Tags[0] = "mutated"
		source.Host = "mutated.invalid"
	}
	prepared, err := vault.prepareManual(context.Background(), source, credentials)
	if err != nil || prepared.source.Tags[0] != "original" || !reflect.DeepEqual(prepared.credentials, want) {
		t.Fatal("snapshot callback changed frozen source or policy")
	}
	counted.afterRead = nil
	source = prepared.source
	credentials = CloneCredentials(want)
	coordinator := integratedTLSCoordinator(t, vault, func(_ context.Context, request Connection) (ConnectionDiagnostic, error) {
		if !reflect.DeepEqual(request.Credentials, want) || request.Credentials.TLS == credentials.TLS {
			t.Fatal("tester shared caller's policy")
		}
		request.Credentials.TLS.Mode, request.Credentials.TLS.ServerName = "prefer", "tester-mutated.invalid"
		credentials.TLS.Mode, source.Host = "disable", "moved.invalid"
		return ConnectionDiagnostic{Connected: true}, nil
	})
	if _, err := coordinator.ManualConnect(context.Background(), source, credentials); err != nil {
		t.Fatal(err)
	}
	record := assertIntegratedTLSRecord(t, store, keys, prepared.source, want)
	if record.Endpoint != "synthetic.invalid:5432/fixture" {
		t.Fatal("tester changed saved target")
	}
}

func TestCredentialTLSCASConflictNeverReinheritsOrRetests(t *testing.T) {
	for _, initiallySaved := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "legacy"}[initiallySaved], func(t *testing.T) {
			store, keys := revisionTestStore(t), activationKeys(t)
			writer, source := activationVault(t, store, keys), activationSource("source")
			if initiallySaved {
				activationSave(t, writer, source, activationCredentials("old"))
			}
			counted := &activationStore{CredentialStore: store}
			winner := integratedTLSCredentials("winner")
			tests := 0
			coordinator := integratedTLSCoordinator(t, activationVault(t, counted, keys), func(_ context.Context, request Connection) (ConnectionDiagnostic, error) {
				tests++
				if request.Credentials.TLS != nil {
					t.Fatal("late request changed its original policy")
				}
				activationSave(t, writer, source, winner)
				return ConnectionDiagnostic{Connected: true}, nil
			})
			diagnostic, err := coordinator.ManualConnect(context.Background(), source, activationCredentials("late"))
			if ErrorCodeOf(err) != CodeCredentialConflict || !diagnostic.Connected || diagnostic.Operation != "manual_persist" ||
				tests != 1 || counted.reads != 1 || counted.writes != 1 || counted.resets != 0 {
				t.Fatalf("CAS conflict rebaselined, retested or hid test success: %v", err)
			}
			assertIntegratedTLSRecord(t, store, keys, source, winner)
		})
	}
}

func TestCredentialTLSRotationPreservesVersionsAndConcurrentWinner(t *testing.T) {
	store, keys := revisionTestStore(t), activationKeys(t)
	vault := activationVault(t, store, keys)
	legacySource, strictSource := activationSource("legacy"), activationSource("strict")
	activationSave(t, vault, legacySource, activationCredentials("legacy"))
	strict := integratedTLSCredentials("strict")
	activationSave(t, vault, strictSource, strict)
	if err := vault.DisableAutomatic(context.Background(), strictSource.ID, CodeAuthFailed); err != nil {
		t.Fatal(err)
	}
	before := revisionSnapshot(t, store, strictSource.ID)
	if err := vault.Rotate(context.Background()); err != nil {
		t.Fatal(err)
	}
	legacy := revisionSnapshot(t, store, legacySource.ID)
	if versionedEnvelope(t, legacy.Record.Ciphertext).Version != 1 {
		t.Fatal("nil-policy rotation must remain v1")
	}
	after := assertIntegratedTLSRecord(t, store, keys, strictSource, strict)
	if after.AutomaticEnabled || after.LastErrorCode != CodeAuthFailed || after.KeyVersion <= before.Record.KeyVersion {
		t.Fatal("rotation changed disabled state or did not advance the key")
	}
	counted := &activationStore{CredentialStore: store}
	winner := integratedTLSCredentials("winner")
	counted.beforeRotate = func() { activationSave(t, vault, strictSource, winner) }
	if err := activationVault(t, counted, keys).Rotate(context.Background()); ErrorCodeOf(err) != CodeKeyRotationFailed {
		t.Fatalf("expected frozen rotation conflict: %v", err)
	}
	assertIntegratedTLSRecord(t, store, keys, strictSource, winner)
	currentLegacy := revisionSnapshot(t, store, legacySource.ID)
	if !bytes.Equal(legacy.Record.Ciphertext, currentLegacy.Record.Ciphertext) || currentLegacy.Record.Revision != legacy.Record.Revision || counted.resets != 0 {
		t.Fatal("rotation conflict partially applied or disabled an unchanged record")
	}
}

func TestCredentialTLSNilCoordinatorFailsSafely(t *testing.T) {
	var coordinator *ConnectionCoordinator
	source := activationSource("source")
	for _, run := range []func() error{
		func() error { _, err := coordinator.Connect(context.Background(), source, nil); return err },
		func() error {
			_, err := coordinator.ManualConnect(context.Background(), source, activationCredentials("manual"))
			return err
		},
		func() error {
			_, err := coordinator.TestConnection(context.Background(), source, activationCredentials("manual"))
			return err
		},
		func() error { _, err := coordinator.ResolveConnection(context.Background(), source, nil); return err },
	} {
		if err := run(); ErrorCodeOf(err) != CodeCredentialStoreUnavailable {
			t.Fatal("typed nil coordinator did not fail closed")
		}
	}
}

func TestCredentialTLSEndpointMatchesConnectorSelection(t *testing.T) {
	source := activationSource("source")
	source.Location = "ignored.invalid:6543/ignored-database"
	if got := sourceEndpoint(source, "requested-database"); got != "synthetic.invalid:6543/requested-database" {
		t.Fatalf("vault target differs from actual connector target: %s", got)
	}
	source.Host = ""
	if got := sourceEndpoint(source, "requested-database"); got != "ignored.invalid:6543/requested-database" {
		t.Fatalf("location-only target differs from actual connector: %s", got)
	}
}

func TestCredentialTLSInvalidExplicitPolicyCannotRepair(t *testing.T) {
	for _, policy := range []*TLSPolicy{{}, {Mode: "prefer"}, {Mode: TLSVerifyFull, CAPEM: "bad-ca"}, {Mode: TLSVerifyFull, ServerName: "*.invalid"}} {
		t.Run(string(policy.Mode)+policy.CAPEM+policy.ServerName, func(t *testing.T) {
			store, keys := revisionTestStore(t), activationKeys(t)
			vault, source := activationVault(t, store, keys), activationSource("source")
			activationSave(t, vault, source, integratedTLSCredentials("saved"))
			revisionExec(t, store, `UPDATE ncp_database_credentials SET ciphertext = X'01' WHERE source_id = 'source'`)
			before := revisionSnapshot(t, store, source.ID)
			counted := &activationStore{CredentialStore: store}
			coordinator := integratedTLSCoordinator(t, activationVault(t, counted, keys), func(context.Context, Connection) (ConnectionDiagnostic, error) {
				t.Fatal("invalid explicit repair reached tester")
				return ConnectionDiagnostic{}, nil
			})
			credentials := activationCredentials("new")
			credentials.TLS = policy
			if _, err := coordinator.ManualConnect(context.Background(), source, credentials); ErrorCodeOf(err) != CodeTLSConfigurationInvalid {
				t.Fatalf("invalid explicit repair was not rejected: %v", err)
			}
			if counted.reads != 0 || counted.writes != 0 || counted.resets != 0 {
				t.Fatal("invalid explicit policy crossed the target-validation boundary")
			}
			revisionAssertUnchanged(t, store, before)
		})
	}
}
