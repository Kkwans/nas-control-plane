package docker

import (
	"context"
	"errors"
	"io"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
)

type fakeInventoryEventsClient struct {
	events func(context.Context, client.EventsListOptions) client.EventsResult
	closed atomic.Bool
}

func (f *fakeInventoryEventsClient) Events(ctx context.Context, options client.EventsListOptions) client.EventsResult {
	return f.events(ctx, options)
}

func (f *fakeInventoryEventsClient) Close() error {
	f.closed.Store(true)
	return nil
}

func fakeInventoryEventSource(api *fakeInventoryEventsClient) *InventoryEventSource {
	return &InventoryEventSource{
		newClient: func() (inventoryEventsClient, error) { return api, nil },
		afterFunc: func(time.Duration, func()) func() bool { return func() bool { return true } },
	}
}

func awaitInventoryEventResult[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("fake event barrier timed out")
		var zero T
		return zero
	}
}

func TestDockerRealtimeEventSourceFiltersAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	messages := make(chan events.Message)
	errorsChannel := make(chan error)
	optionsSeen := make(chan client.EventsListOptions, 1)
	readerExited := make(chan struct{})
	api := &fakeInventoryEventsClient{events: func(ctx context.Context, options client.EventsListOptions) client.EventsResult {
		optionsSeen <- options
		go func() {
			<-ctx.Done()
			close(readerExited)
			close(errorsChannel)
		}()
		return client.EventsResult{Messages: messages, Err: errorsChannel}
	}}
	notifications := make(chan bool, 2)
	result := make(chan error, 1)
	go func() {
		result <- fakeInventoryEventSource(api).Watch(ctx, func(ready bool) { notifications <- ready })
	}()
	options := awaitInventoryEventResult(t, optionsSeen)
	filters := make(client.Filters)
	filters.Add("type", "container")
	filters.Add("type", "image")
	if !reflect.DeepEqual(options, client.EventsListOptions{Filters: filters}) {
		t.Fatalf("unexpected filters or replay options: %#v", options)
	}
	if !awaitInventoryEventResult(t, notifications) {
		t.Fatal("missing attempt readiness")
	}
	messages <- events.Message{Action: "health_status: healthy", Actor: events.Actor{Attributes: map[string]string{"secret": "must not leave source"}}}
	if awaitInventoryEventResult(t, notifications) {
		t.Fatal("raw Docker event should only invalidate")
	}
	cancel()
	if err := awaitInventoryEventResult(t, result); !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		t.Fatalf("cancel result = %v", err)
	}
	awaitInventoryEventResult(t, readerExited)
	if !api.closed.Load() {
		t.Fatal("per-attempt client was not closed")
	}
}

func TestDockerRealtimeEventSourceTerminalWithoutMessageClose(t *testing.T) {
	for _, test := range []struct {
		name        string
		err         error
		beforeReady bool
	}{
		{"error before ready", errors.New("private Engine detail"), true},
		{"closed error channel before ready", nil, true},
		{"EOF after ready", io.EOF, false},
		{"closed error channel after ready", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			messages := make(chan events.Message) // SDK 不会关闭 Messages。
			errorsChannel := make(chan error, 1)
			terminate := func() {
				if test.err != nil {
					errorsChannel <- test.err
				}
				close(errorsChannel)
			}
			if test.beforeReady {
				terminate()
			}
			api := &fakeInventoryEventsClient{events: func(context.Context, client.EventsListOptions) client.EventsResult {
				return client.EventsResult{Messages: messages, Err: errorsChannel}
			}}
			notifications := make(chan bool, 2)
			result := make(chan error, 1)
			go func() {
				result <- fakeInventoryEventSource(api).Watch(context.Background(), func(ready bool) { notifications <- ready })
			}()
			if !test.beforeReady {
				if !awaitInventoryEventResult(t, notifications) {
					t.Fatal("missing readiness")
				}
				terminate()
			}
			err := awaitInventoryEventResult(t, result)
			expected := test.err
			if expected == nil {
				expected = io.EOF
			}
			if !errors.Is(err, expected) {
				t.Fatalf("got %v, want %v", err, expected)
			}
			if len(notifications) != 0 {
				t.Fatal("source emitted a notification after known termination")
			}
			if !api.closed.Load() {
				t.Fatal("client not closed")
			}
		})
	}
}

func TestDockerRealtimeEventSourceEstablishmentTimeoutAndJoin(t *testing.T) {
	started := make(chan struct{})
	exitAllowed := make(chan struct{})
	api := &fakeInventoryEventsClient{events: func(ctx context.Context, _ client.EventsListOptions) client.EventsResult {
		close(started)
		<-ctx.Done()
		<-exitAllowed
		errorsChannel := make(chan error)
		close(errorsChannel)
		return client.EventsResult{Messages: make(chan events.Message), Err: errorsChannel}
	}}
	source := fakeInventoryEventSource(api)
	var fire func()
	source.afterFunc = func(duration time.Duration, callback func()) func() bool {
		if duration != 5*time.Second {
			t.Errorf("establish budget = %v", duration)
		}
		fire = callback
		return func() bool { return false }
	}
	result := make(chan error, 1)
	go func() {
		result <- source.Watch(context.Background(), func(bool) { t.Error("timed-out attempt emitted readiness") })
	}()
	awaitInventoryEventResult(t, started)
	fire()
	select {
	case <-result:
		t.Fatal("Watch returned before canceled establishment joined")
	default:
	}
	close(exitAllowed)
	if err := awaitInventoryEventResult(t, result); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout = %v", err)
	}
	if !api.closed.Load() {
		t.Fatal("timed-out client not closed")
	}
}

func TestDockerRealtimeEventSourceCancelJoinsReader(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errorsChannel := make(chan error)
	canceled := make(chan struct{})
	exitAllowed := make(chan struct{})
	api := &fakeInventoryEventsClient{events: func(ctx context.Context, _ client.EventsListOptions) client.EventsResult {
		go func() {
			<-ctx.Done()
			close(canceled)
			<-exitAllowed
			close(errorsChannel)
		}()
		return client.EventsResult{Messages: make(chan events.Message), Err: errorsChannel}
	}}
	ready := make(chan struct{}, 1)
	result := make(chan error, 1)
	go func() { result <- fakeInventoryEventSource(api).Watch(ctx, func(bool) { ready <- struct{}{} }) }()
	awaitInventoryEventResult(t, ready)
	cancel()
	awaitInventoryEventResult(t, canceled)
	select {
	case <-result:
		t.Fatal("Watch returned before SDK reader joined")
	default:
	}
	close(exitAllowed)
	_ = awaitInventoryEventResult(t, result)
	if !api.closed.Load() {
		t.Fatal("client not closed after join")
	}
}

func TestDockerRealtimeEventSourceNeverDefaultsToLive(t *testing.T) {
	for _, source := range []*InventoryEventSource{nil, {}} {
		if err := source.Watch(context.Background(), func(bool) {}); err == nil {
			t.Fatal("missing injected source was accepted")
		}
	}
}

func TestDockerRealtimeEventSourceEstablishmentCancellationJoins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	canceled := make(chan struct{})
	allowExit := make(chan struct{})
	api := &fakeInventoryEventsClient{events: func(ctx context.Context, _ client.EventsListOptions) client.EventsResult {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-allowExit
		errorsChannel := make(chan error)
		close(errorsChannel)
		return client.EventsResult{Messages: make(chan events.Message), Err: errorsChannel}
	}}
	result := make(chan error, 1)
	go func() {
		result <- fakeInventoryEventSource(api).Watch(ctx, func(bool) { t.Error("canceled handshake emitted readiness") })
	}()
	awaitInventoryEventResult(t, started)
	cancel()
	awaitInventoryEventResult(t, canceled)
	select {
	case <-result:
		t.Fatal("canceled establishment did not join")
	default:
	}
	close(allowExit)
	if err := awaitInventoryEventResult(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("handshake canceled = %v", err)
	}
	if !api.closed.Load() {
		t.Fatal("canceled establishment did not close client")
	}
}

func TestDockerRealtimeEventSourceTerminalPrioritizedOverStorm(t *testing.T) {
	messages := make(chan events.Message, 100)
	errorsChannel := make(chan error, 1)
	api := &fakeInventoryEventsClient{events: func(context.Context, client.EventsListOptions) client.EventsResult {
		return client.EventsResult{Messages: messages, Err: errorsChannel}
	}}
	ready := make(chan struct{})
	resumeReader := make(chan struct{})
	var invalidations atomic.Int32
	result := make(chan error, 1)
	go func() {
		result <- fakeInventoryEventSource(api).Watch(context.Background(), func(isReady bool) {
			if isReady {
				close(ready)
				<-resumeReader
			} else {
				invalidations.Add(1)
			}
		})
	}()
	awaitInventoryEventResult(t, ready)
	for range cap(messages) {
		messages <- events.Message{}
	}
	errorsChannel <- io.EOF
	close(errorsChannel)
	close(resumeReader)
	if err := awaitInventoryEventResult(t, result); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if invalidations.Load() != 0 {
		t.Fatalf("sent %d invalidations after known terminal", invalidations.Load())
	}
}
