package terminal

import (
	"context"
	"errors"
	"testing"
)

func TestTerminalProductionCapacityFactory(t *testing.T) {
	for _, capacity := range []int{-1, 0, 1, 2, 3, 4, 5} {
		calls := 0
		host, container := reservationStarter(func(_ context.Context, _ StartRequest) (Session, error) { return &reservationSession{}, nil }), reservationStarter(func(_ context.Context, _ StartRequest) (Session, error) { return &reservationSession{}, nil })
		manager, err := newProductionManager(capacity, func() Starter { calls++; return host }, func() (Starter, error) { calls++; return container, nil })
		if capacity < 1 || capacity > 4 {
			if ErrorCode(err) != "TERMINAL_MAX_SESSIONS_INVALID" || manager != nil || calls != 0 {
				t.Fatalf("invalid %d: manager=%v err=%v calls=%d", capacity, manager, err, calls)
			}
		} else if err != nil || manager.maxSessions != capacity || calls != 2 {
			t.Fatalf("capacity %d: manager=%v err=%v calls=%d", capacity, manager, err, calls)
		}
	}
	cause := errors.New("fake constructor failure")
	hostCalled := false
	manager, err := newProductionManager(4, func() Starter { hostCalled = true; return nil }, func() (Starter, error) { return nil, cause })
	if manager != nil || err != cause || hostCalled {
		t.Fatal("constructor error not preserved")
	}
	if DefaultMaxSessions != 1 {
		t.Fatal("POC/default capacity changed")
	}
}
