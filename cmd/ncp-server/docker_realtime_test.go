package main

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestDockerRealtimeShutdownSharesOwnerBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		jobs := &fakeMetricsJobOwner{close: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
		summary := fakeMetricsCollector{make(chan struct{})}
		docker := fakeMetricsCollector{make(chan struct{})}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		start := time.Now()
		err := shutdownOwners(ctx, nil, jobs, summary, true, docker)
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 10*time.Second {
			t.Fatalf("err=%v duration=%s", err, time.Since(start))
		}
	})
}
func TestDockerRealtimeShutdownWaitsForDocker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		jobs := &fakeMetricsJobOwner{close: func(context.Context) error { return nil }}
		summary := fakeMetricsCollector{make(chan struct{})}
		close(summary.done)
		docker := fakeMetricsCollector{make(chan struct{})}
		go func() { time.Sleep(time.Second); close(docker.done) }()
		start := time.Now()
		if err := shutdownOwners(context.Background(), nil, jobs, summary, true, docker); err != nil {
			t.Fatal(err)
		}
		if time.Since(start) != time.Second {
			t.Fatal("returned before docker owner stopped")
		}
	})
}
