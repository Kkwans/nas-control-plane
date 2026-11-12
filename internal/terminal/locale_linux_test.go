//go:build linux

package terminal

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLocaleProbeFakeResults(t *testing.T) {
	for _, tc := range []struct {
		name, output, want string
		err                error
	}{
		{"utf8", "C\nC.utf8\nPOSIX\n", "C.UTF-8", nil},
		{"caseWhitespace", "\t c.UtF-8 \r\n", "C.UTF-8", nil},
		{"empty", "", "C", nil},
		{"plain", "C POSIX en_US.UTF-8 c.utf8evil prefixc.utf-8", "C", nil},
		{"nonzero", "C.utf8", "C", errors.New("exit status 7")},
		{"waitDelay", "C.utf8", "C", exec.ErrWaitDelay},
		{"deadlineError", "C.utf8", "C", context.DeadlineExceeded},
		{"cancelError", "C.utf8", "C", context.Canceled},
		{"exactLimit", "C.utf8" + strings.Repeat(" ", localeOutputLimit-6), "C.UTF-8", nil},
		{"overflowAfterToken", "C.utf8" + strings.Repeat(" ", localeOutputLimit), "C", nil},
		{"overflowBeforeToken", strings.Repeat(" ", localeOutputLimit) + "C.utf8", "C", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := supportedTerminalLocaleWithRunner(nil, func(_ context.Context, output io.Writer) error {
				_, _ = io.WriteString(output, tc.output)
				return tc.err // 故意忽略 writer error，仍必须因 overflow 回退。
			})
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLocaleProbeFakeCanceledBeforeRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := supportedTerminalLocaleWithRunner(ctx, func(context.Context, io.Writer) error {
		t.Fatal("canceled probe called runner")
		return nil
	})
	if got != "C" {
		t.Fatal(got)
	}
}

func TestLocaleProbeFakeCancelAfterSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := supportedTerminalLocaleWithRunner(ctx, func(_ context.Context, output io.Writer) error {
		_, _ = io.WriteString(output, "C.utf8")
		cancel()
		return nil
	})
	if got != "C" {
		t.Fatal(got)
	}
}

func TestLocaleProbeFakeParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, exited := make(chan struct{}), make(chan struct{})
	result := make(chan string, 1)
	go func() {
		result <- supportedTerminalLocaleWithRunner(ctx, func(ctx context.Context, output io.Writer) error {
			defer close(exited)
			_, _ = io.WriteString(output, "C.utf8")
			close(entered)
			<-ctx.Done()
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("fake did not enter")
	}
	cancel()
	select {
	case got := <-result:
		if got != "C" {
			t.Fatal(got)
		}
		select {
		case <-exited:
		default:
			t.Fatal("runner has not exited")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fake did not return")
	}
}

func TestLocaleProbeFakeBudget(t *testing.T) {
	for _, shortParent := range []bool{false, true} {
		t.Run(map[bool]string{false: "own", true: "parent"}[shortParent], func(t *testing.T) {
			parent := context.Background()
			var parentDeadline time.Time
			if shortParent {
				var cancel context.CancelFunc
				parentDeadline = time.Now().Add(200 * time.Millisecond)
				parent, cancel = context.WithDeadline(parent, parentDeadline)
				defer cancel()
			}
			before := time.Now()
			var observed context.Context
			got := supportedTerminalLocaleWithRunner(parent, func(ctx context.Context, _ io.Writer) error {
				observed = ctx
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Fatal("missing deadline")
				}
				if shortParent && !deadline.Equal(parentDeadline) {
					t.Fatalf("lost parent deadline: %v", deadline)
				}
				if !shortParent && (deadline.Before(before.Add(localeProbeTimeout)) || deadline.After(time.Now().Add(localeProbeTimeout))) {
					t.Fatalf("wrong probe deadline: %v", deadline)
				}
				return nil
			})
			if got != "C" || observed.Err() != context.Canceled {
				t.Fatalf("got %q; child context not canceled: %v", got, observed.Err())
			}
		})
	}
}

func TestLocaleProbeFakeDeadlineExpiry(t *testing.T) {
	result := make(chan string, 1)
	go func() {
		result <- supportedTerminalLocaleWithRunner(context.Background(), func(ctx context.Context, output io.Writer) error {
			_, _ = io.WriteString(output, "C.utf8")
			<-ctx.Done()
			return nil
		})
	}()
	select {
	case got := <-result:
		if got != "C" {
			t.Fatal(got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("probe deadline did not expire")
	}
}

func TestLocaleOutputLimit(t *testing.T) {
	for _, size := range []int{0, localeOutputLimit - 1, localeOutputLimit, localeOutputLimit + 1, localeOutputLimit * 128} {
		output := new(localeOutput)
		p := []byte(strings.Repeat("x", size))
		n, err := output.Write(p)
		want := min(size, localeOutputLimit)
		overflow := size > localeOutputLimit
		if n != want || output.n != want || len(output.data) != localeOutputLimit || cap(output.data[:]) != localeOutputLimit || output.overflow != overflow || errors.Is(err, errLocaleOutputLimit) != overflow {
			t.Fatalf("size %d: n=%d stored=%d overflow=%v err=%v", size, n, output.n, output.overflow, err)
		}
		if string(output.data[:output.n]) != strings.Repeat("x", want) {
			t.Fatal("wrong retained bytes")
		}
		if overflow {
			for _, retry := range [][]byte{nil, []byte("retry")} {
				if n, err := output.Write(retry); n != 0 || err != errLocaleOutputLimit || output.n != localeOutputLimit {
					t.Fatal("accepted data after overflow")
				}
			}
		}
	}
}

func TestLocaleOutputLimitMultipleWrites(t *testing.T) {
	output := new(localeOutput)
	if n, err := output.Write([]byte(strings.Repeat("a", localeOutputLimit-2))); n != localeOutputLimit-2 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := output.Write([]byte("bcde")); n != 2 || err != errLocaleOutputLimit {
		t.Fatal(n, err)
	}
	if output.n != localeOutputLimit || !output.overflow || string(output.data[localeOutputLimit-2:]) != "bc" {
		t.Fatal("incorrect partial write")
	}
}

func TestLocaleProbeFakeNoCache(t *testing.T) {
	for _, output := range []string{"C", "C.utf8", "C", "C.utf-8"} {
		want := "C"
		if output != "C" {
			want = "C.UTF-8"
		}
		got := supportedTerminalLocaleWithRunner(nil, func(_ context.Context, w io.Writer) error { _, err := io.WriteString(w, output); return err })
		if got != want {
			t.Fatal(got, want)
		}
	}
	if got := supportedTerminalLocaleWithRunner(nil, func(context.Context, io.Writer) error { return errors.New("failed") }); got != "C" {
		t.Fatal(got)
	}
	if got := supportedTerminalLocaleWithRunner(nil, func(_ context.Context, w io.Writer) error { _, err := io.WriteString(w, "C.utf8"); return err }); got != "C.UTF-8" {
		t.Fatal(got)
	}
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			text, want := "C", "C"
			if i%2 == 0 {
				text, want = "C.utf8", "C.UTF-8"
			}
			got := supportedTerminalLocaleWithRunner(nil, func(_ context.Context, w io.Writer) error { _, err := io.WriteString(w, text); return err })
			if got != want {
				t.Errorf("concurrent got %q want %q", got, want)
			}
		}(i)
	}
	group.Wait()
}

func TestLocaleCommandConfig(t *testing.T) {
	output := new(localeOutput)
	cmd := newLocaleCommand(context.Background(), output)
	if !reflect.DeepEqual(cmd.Args, []string{"locale", "-a"}) || cmd.WaitDelay != 100*time.Millisecond || cmd.Stdout != output || cmd.Stderr != nil || cmd.Env != nil || cmd.Dir != "" || cmd.SysProcAttr != nil || len(cmd.ExtraFiles) != 0 || cmd.Cancel == nil {
		t.Fatal("locale command configuration changed")
	}
}
