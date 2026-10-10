package agentsocket

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/system"
)

func TestRecoverableProxyBackoffRecoveryAndRedactedLogs(t *testing.T) {
	now := time.Unix(100, 0)
	attempts := 0
	var logs bytes.Buffer
	p := newRecoverableProxyProvider(func() (ProxyProvider, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("private-token-must-not-leak")
		}
		return fakeProxyProvider{capability: system.MihomoCapability{State: "available"}}, nil
	})
	p.now = func() time.Time { return now }
	p.logger = slog.New(slog.NewTextHandler(&logs, nil))
	for range 20 {
		if _, err := p.ProbeMihomo(context.Background()); !errors.Is(err, errProxyInitialization) {
			t.Fatalf("error = %v", err)
		}
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d", attempts)
	}
	now = now.Add(time.Second)
	_, _ = p.InspectMihomo(context.Background(), true)
	if attempts != 2 || p.backoff != 2*time.Second {
		t.Fatalf("attempts/backoff = %d/%v", attempts, p.backoff)
	}
	now = now.Add(2 * time.Second)
	v, err := p.ProbeMihomo(context.Background())
	if err != nil || v.State != "available" {
		t.Fatalf("recovery = %v / %v", v, err)
	}
	_, _ = p.InvokeMihomo(context.Background(), system.MihomoInvokeRequest{})
	if attempts != 3 {
		t.Fatal("successful provider must be reused")
	}
	if strings.Contains(logs.String(), "private-token") || !strings.Contains(logs.String(), "recovered") {
		t.Fatalf("unsafe/missing log: %s", &logs)
	}
}

func TestRecoverableProxyConcurrentFailuresAreBounded(t *testing.T) {
	attempts := 0
	p := newRecoverableProxyProvider(func() (ProxyProvider, error) { attempts++; return nil, errors.New("missing") })
	var group sync.WaitGroup
	for range 50 {
		group.Go(func() { _, _ = p.ProbeMihomo(context.Background()) })
	}
	group.Wait()
	if attempts != 1 {
		t.Fatalf("concurrent initialization attempts = %d", attempts)
	}
	for range 20 {
		p.nextRetry = time.Time{}
		_, _ = p.current(context.Background())
		if p.backoff > time.Minute {
			t.Fatalf("backoff exceeds limit: %v", p.backoff)
		}
	}
	if p.backoff != time.Minute {
		t.Fatalf("backoff = %v", p.backoff)
	}
}

func TestRecoverableProxyHonorsCancellation(t *testing.T) {
	p := newRecoverableProxyProvider(func() (ProxyProvider, error) { t.Fatal("canceled request must not initialize"); return nil, nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.ProbeMihomo(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestRecoverableProxyReloadsRestoredConfiguration(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "active.yaml")
	p := newRecoverableProxyProvider(func() (ProxyProvider, error) {
		return NewLiveProxyProviderFromConfig(system.NewOSEnvironment(), configPath, "", "", "", "")
	})
	if _, err := p.current(context.Background()); err == nil {
		t.Fatal("missing configuration must fail")
	}
	if err := os.WriteFile(configPath, []byte("external-controller: 127.0.0.1:9091\nmixed-port: 7890\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p.nextRetry = time.Time{}
	if _, err := p.current(context.Background()); err != nil {
		t.Fatalf("configuration restoration must recover: %v", err)
	}
}
