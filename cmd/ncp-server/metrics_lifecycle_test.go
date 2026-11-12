package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeMetricsJobOwner struct {
	admissionStopped atomic.Bool
	close            func(context.Context) error
}

func (f *fakeMetricsJobOwner) StopAdmission() { f.admissionStopped.Store(true) }
func (f *fakeMetricsJobOwner) Close(ctx context.Context) error {
	if !f.admissionStopped.Load() {
		panic("admission not stopped")
	}
	return f.close(ctx)
}

type fakeMetricsCollector struct{ done chan struct{} }

func (f fakeMetricsCollector) Done() <-chan struct{} { return f.done }
func TestMetricsShutdownSharedBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	jobs := &fakeMetricsJobOwner{close: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	collector := fakeMetricsCollector{make(chan struct{})}
	start := time.Now()
	err := shutdownOwners(ctx, nil, jobs, collector, true)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("shutdown exceeded shared budget")
	}
}
func TestMetricsShutdownWaitsBeforeReturning(t *testing.T) {
	done := make(chan struct{})
	collector := fakeMetricsCollector{done}
	jobs := &fakeMetricsJobOwner{close: func(context.Context) error { close(done); return nil }}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := shutdownOwners(ctx, nil, jobs, collector, true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	default:
		t.Fatal("returned with collector alive")
	}
}
