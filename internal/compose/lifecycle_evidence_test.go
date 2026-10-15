package compose

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

func TestParseComposePSRejectsUntrustworthyEvidence(t *testing.T) {
	for _, raw := range []string{
		`null`, `[null]`, `[{}]`, `{"ID":"a","State":   null  }`, `{"ID":"a","State":"running","Running":   null  }`, `{"ID":"a"}`, `{"ID":"a","State":null}`, `{"ID":"a","State":" "}`,
		`{"ID":"a","Running":false}`, `{"ID":"a","Running":true}`, `{"State":"running"}`,
		`{"ID":null,"State":"running"}`, `{"ID":4,"State":"running"}`, `{"ID":"a","Name":null,"State":"running"}`,
		`{"ID":"a","Service":false,"State":"running"}`, `{"ID":"a","State":false}`,
		`{"ID":"a","State":"running","Running":false}`, `{"ID":"a","State":"exited","Running":true}`,
		`{"ID":"a","State":"created","Running":true}`, `{"ID":"a","State":"dead","Running":true}`,
		`{"ID":"a","State":"stopped","Running":true}`, `{"ID":"a","State":"running","Running":null}`,
		`{"ID":"a","State":"running","Running":"true"}`, `{"ID":"a","State":"exited","State":"running"}`,
		`{"ID":"a","State":"exited","state":"running"}`, `{"id":"a","State":"running"}`, `{"ID":"a","State":"running","ſtate":"exited"}`,
		`{"ID":"a","ID":"b","State":"running"}`, `{"ID":"a","State":"running","Running":true,"running":true}`,
		`[{"ID":"a","State":"running"},{"ID":" a ","State":"running"}]`,
		"{\"ID\":\"a\",\"State\":\"running\"}\nnull",
		`{"ID":"a","State":"running"} {"ID":"b","State":"running"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			got, err := parseComposePS(raw)
			if err == nil || got != nil {
				t.Fatalf("accepted invalid evidence: %#v, %v", got, err)
			}
		})
	}
}

func TestParseComposePSPreservesCompatibleEvidence(t *testing.T) {
	for _, format := range []string{"array", "lines"} {
		t.Run(format, func(t *testing.T) {
			a := `{"ID":" a ","Service":"web","Name":"demo-web-1","State":" RUNNING ","Running":true,"Health":"healthy","Extra":{"State":null}}`
			b := `{"ID":"b","Service":"web","Name":"demo-web-2","State":"exited","Running":false}`
			raw := "[" + a + "," + b + "]"
			if format == "lines" {
				raw = "\n" + a + "\n\n" + b + "\n"
			}
			got, err := parseComposePS(raw)
			want := []LifecycleServiceStatus{{Name: "web", ContainerID: "a", State: "running", Running: true}, {Name: "web", ContainerID: "b", State: "exited"}}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v, %v; want %#v", got, err, want)
			}
		})
	}
	for _, test := range []struct {
		raw  string
		want LifecycleServiceStatus
	}{
		{`{"Name":"/demo-web-1","State":"running"}`, LifecycleServiceStatus{Name: "demo-web-1", State: "running", Running: true}},
		{`{"ID":"short","State":"exited"}`, LifecycleServiceStatus{ContainerID: "short", State: "exited"}},
		{`{"Service":"web","State":"future","Running":true}`, LifecycleServiceStatus{Name: "web", State: "future"}},
		{`{"Service":"web","State":"paused","Running":true}`, LifecycleServiceStatus{Name: "web", State: "paused"}},
	} {
		got, err := parseComposePS(test.raw)
		if err != nil || !reflect.DeepEqual(got, []LifecycleServiceStatus{test.want}) {
			t.Fatalf("parse %s: %#v, %v", test.raw, got, err)
		}
	}
	for _, raw := range []string{"", " \n", "[]"} {
		got, err := parseComposePS(raw)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("empty evidence: %#v %v", got, err)
		}
	}
}

func TestLifecycleRequiresExplicitObservedState(t *testing.T) {
	for _, state := range []string{"running", "exited", "created", "dead", "stopped", "paused", "restarting", "removing", "future", ""} {
		for _, running := range []bool{false, true} {
			for _, action := range []LifecycleAction{LifecycleActionStart, LifecycleActionStop, LifecycleActionRestart} {
				t.Run(fmt.Sprintf("%s/%s/%t", action, state, running), func(t *testing.T) {
					runner := &lifecycleRunnerStub{services: []LifecycleServiceStatus{{Name: "web", State: state, Running: running}}}
					before := append([]LifecycleServiceStatus(nil), runner.services...)
					got, err := NewManager(runner).Lifecycle(context.Background(), LifecycleRequest{ProjectID: "compose:demo", WorkingDirectory: "/volume2/Project/demo", ConfigFiles: []string{"compose.yaml"}, Action: action})
					stopped := state == "exited" || state == "created" || state == "dead" || state == "stopped"
					wantCompleted := (action == LifecycleActionStop && stopped) || (action != LifecycleActionStop && state == "running")
					wantState := "unknown"
					if state == "running" {
						wantState = "running"
					} else if stopped {
						wantState = "stopped"
					}
					if got.Completed != wantCompleted || (err == nil) != wantCompleted || got.State != wantState {
						t.Fatalf("got %#v, %v; completed=%t state=%s", got, err, wantCompleted, wantState)
					}
					if err != nil && ErrorCode(err) != "COMPOSE_LIFECYCLE_VERIFY_FAILED" {
						t.Fatalf("unexpected error %v", err)
					}
					if !reflect.DeepEqual(runner.calls, []string{string(action), "inspect"}) || !reflect.DeepEqual(runner.services, before) {
						t.Fatalf("calls/input changed: %#v", runner)
					}
				})
			}
		}
	}
}

func TestLifecycleStateDoesNotHideUnknownEvidence(t *testing.T) {
	for _, test := range []struct {
		states []string
		want   string
	}{
		{nil, "unknown"}, {[]string{"running", "exited"}, "degraded"}, {[]string{"running", "future"}, "unknown"},
		{[]string{"exited", "paused"}, "unknown"}, {[]string{"created", "dead"}, "stopped"}, {[]string{"running", "running"}, "running"},
	} {
		var services []LifecycleServiceStatus
		for _, state := range test.states {
			services = append(services, LifecycleServiceStatus{Name: "replica", State: state})
		}
		got := lifecycleState(normalizeServiceStatuses(services))
		if got != test.want {
			t.Fatalf("%v: %s want %s", test.states, got, test.want)
		}
	}
}
