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

const bashCapabilityFakeMarkers = "NCP_READLINE=1\nNCP_BLE_SH=1\nset enable-bracketed-paste on\n"

func requireBashCapabilityFailure(t *testing.T, p shellCapabilityProbe, cause error) {
	t.Helper()
	if p.Readline || p.BracketedPaste || p.BleSH || p.Err == nil || (cause != nil && !errors.Is(p.Err, cause)) {
		t.Fatalf("nonconservative failure: %#v; want cause %v", p, cause)
	}
}

func TestBashCapabilityFakeResults(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		want         shellCapabilityProbe
	}{
		{"complete", bashCapabilityFakeMarkers, shellCapabilityProbe{Readline: true, BracketedPaste: true, BleSH: true}},
		{"partial", "NCP_READLINE=1", shellCapabilityProbe{Readline: true}},
		{"pasteOnly", "set enable-bracketed-paste on", shellCapabilityProbe{BracketedPaste: true}},
		{"bleOnly", " NCP_BLE_SH=1\r\n", shellCapabilityProbe{BleSH: true}},
		{"empty", "", shellCapabilityProbe{}},
		{"noise", "xNCP_READLINE=1\nNCP_READLINE=10\nNCP_BLE_SH=1suffix\nset enable-bracketed-paste onx\n", shellCapabilityProbe{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := probeBashCapabilitiesWithRunner(nil, "/fake/bash", "", func(_ context.Context, w io.Writer) error { _, err := io.WriteString(w, tc.output); return err })
			if got != tc.want {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
		})
	}
	for _, err := range []error{&exec.ExitError{}, exec.ErrWaitDelay, context.Canceled, context.DeadlineExceeded, exec.ErrNotFound, errors.New("run failure")} {
		got := probeBashCapabilitiesWithRunner(nil, "/fake/bash", "", func(_ context.Context, w io.Writer) error {
			_, _ = io.WriteString(w, bashCapabilityFakeMarkers)
			return err
		})
		requireBashCapabilityFailure(t, got, err)
	}
}

func TestBashCapabilityFakeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		ctx   context.Context
		shell string
	}{{ctx, "/fake/bash"}, {nil, ""}} {
		got := probeBashCapabilitiesWithRunner(tc.ctx, tc.shell, "", func(context.Context, io.Writer) error { t.Fatal("unexpected runner"); return nil })
		requireBashCapabilityFailure(t, got, context.Canceled)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	got := probeBashCapabilitiesWithRunner(ctx, "/fake/bash", "", func(_ context.Context, w io.Writer) error {
		_, _ = io.WriteString(w, bashCapabilityFakeMarkers)
		cancel()
		return nil
	})
	requireBashCapabilityFailure(t, got, context.Canceled)
}

func TestBashCapabilityFakeBudget(t *testing.T) {
	for _, short := range []bool{false, true} {
		parent := context.Background()
		var parentDeadline time.Time
		if short {
			var cancel context.CancelFunc
			parentDeadline = time.Now().Add(200 * time.Millisecond)
			parent, cancel = context.WithDeadline(parent, parentDeadline)
			defer cancel()
		}
		before := time.Now()
		var observed context.Context
		got := probeBashCapabilitiesWithRunner(parent, "/fake/bash", "", func(ctx context.Context, w io.Writer) error {
			observed = ctx
			deadline, ok := ctx.Deadline()
			if !ok || (short && !deadline.Equal(parentDeadline)) || (!short && (deadline.Before(before.Add(750*time.Millisecond)) || deadline.After(time.Now().Add(750*time.Millisecond)))) {
				t.Fatalf("wrong deadline %v", deadline)
			}
			return nil
		})
		if got.Err != nil || observed == nil || !errors.Is(observed.Err(), context.Canceled) {
			t.Fatalf("deferred cancel missing: %#v", got)
		}
	}
}

func TestBashCapabilityFakeDeadline(t *testing.T) {
	for _, short := range []bool{false, true} {
		t.Run(map[bool]string{false: "own", true: "parent"}[short], func(t *testing.T) {
			parent := context.Background()
			if short {
				var cancel context.CancelFunc
				parent, cancel = context.WithTimeout(parent, 20*time.Millisecond)
				defer cancel()
			}
			got := probeBashCapabilitiesWithRunner(parent, "/fake/bash", "", func(ctx context.Context, w io.Writer) error {
				_, _ = io.WriteString(w, bashCapabilityFakeMarkers)
				<-ctx.Done()
				return nil
			})
			requireBashCapabilityFailure(t, got, context.DeadlineExceeded)
		})
	}
}

func TestBashCapabilityOutputLimit(t *testing.T) {
	for _, size := range []int{0, maxCapabilityProbeOutput - 1, maxCapabilityProbeOutput, maxCapabilityProbeOutput + 1, maxCapabilityProbeOutput * 128} {
		output := new(bashCapabilityOutput)
		n, err := output.Write([]byte(strings.Repeat("x", size)))
		want := min(size, maxCapabilityProbeOutput)
		overflow := size > maxCapabilityProbeOutput
		if n != want || output.n != want || len(output.data) != maxCapabilityProbeOutput || cap(output.data[:]) != maxCapabilityProbeOutput || output.overflow != overflow || errors.Is(err, errBashCapabilityOutputLimit) != overflow || string(output.data[:output.n]) != strings.Repeat("x", want) {
			t.Fatalf("size=%d n=%d stored=%d overflow=%v err=%v", size, n, output.n, output.overflow, err)
		}
		if overflow {
			for _, p := range [][]byte{nil, []byte("retry")} {
				if n, err := output.Write(p); n != 0 || err != errBashCapabilityOutputLimit || output.n != maxCapabilityProbeOutput {
					t.Fatal("accepted after overflow")
				}
			}
		}
	}
	output := new(bashCapabilityOutput)
	if n, err := output.Write([]byte(strings.Repeat("a", maxCapabilityProbeOutput-2))); n != maxCapabilityProbeOutput-2 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := output.Write([]byte("bcde")); n != 2 || err != errBashCapabilityOutputLimit {
		t.Fatal(n, err)
	}
	if !output.overflow || string(output.data[maxCapabilityProbeOutput-2:]) != "bc" {
		t.Fatal("wrong retained partial write")
	}
}

func TestBashCapabilityFakeOverflowAndErrors(t *testing.T) {
	for _, text := range []string{bashCapabilityFakeMarkers + strings.Repeat(" ", maxCapabilityProbeOutput), strings.Repeat(" ", maxCapabilityProbeOutput) + bashCapabilityFakeMarkers} {
		got := probeBashCapabilitiesWithRunner(nil, "/fake/bash", "", func(_ context.Context, w io.Writer) error { _, _ = io.WriteString(w, text); return nil })
		requireBashCapabilityFailure(t, got, errBashCapabilityOutputLimit)
	}
	exact := bashCapabilityFakeMarkers + strings.Repeat(" ", maxCapabilityProbeOutput-len(bashCapabilityFakeMarkers))
	got := probeBashCapabilitiesWithRunner(nil, "/fake/bash", "", func(_ context.Context, w io.Writer) error { _, err := io.WriteString(w, exact); return err })
	if got.Err != nil || !got.Readline || !got.BracketedPaste || !got.BleSH {
		t.Fatalf("exact limit failed: %#v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exitErr := &exec.ExitError{}
	got = probeBashCapabilitiesWithRunner(ctx, "/fake/bash", "", func(_ context.Context, w io.Writer) error {
		_, _ = io.WriteString(w, exact+"x")
		cancel()
		return exitErr
	})
	for _, cause := range []error{exitErr, context.Canceled, errBashCapabilityOutputLimit} {
		requireBashCapabilityFailure(t, got, cause)
	}
	var typed *exec.ExitError
	if !errors.As(got.Err, &typed) || typed != exitErr {
		t.Fatal("lost ExitError")
	}
	if strings.Contains(got.Err.Error(), "NCP_READLINE") {
		t.Fatal("output leaked into error")
	}
}

func TestBashCapabilityFakeNoCache(t *testing.T) {
	check := func(fail bool) {
		got := probeBashCapabilitiesWithRunner(nil, "/fake/bash", "", func(_ context.Context, w io.Writer) error {
			_, _ = io.WriteString(w, bashCapabilityFakeMarkers)
			if fail {
				return exec.ErrWaitDelay
			}
			return nil
		})
		if fail {
			requireBashCapabilityFailure(t, got, exec.ErrWaitDelay)
		} else if got.Err != nil || !got.Readline || !got.BracketedPaste || !got.BleSH {
			t.Errorf("success lost: %#v", got)
		}
	}
	for _, fail := range []bool{true, false, true, false} {
		check(fail)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); check(i%2 == 0) }(i)
	}
	wg.Wait()
}

func TestBashCapabilityCommandConfig(t *testing.T) {
	for _, rc := range []string{"", "/tmp/test rc"} {
		output := new(bashCapabilityOutput)
		cmd := newBashCapabilityCommand(context.Background(), "/fake/bash", rc, output)
		args := []string{"/fake/bash", "--noprofile"}
		if rc == "" {
			args = append(args, "--norc")
		} else {
			args = append(args, "--rcfile", rc)
		}
		args = append(args, "-i", "-c", bashCapabilityProbeScript)
		env := []string{"HOME=/root", "USER=root", "LOGNAME=root", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TERM=xterm-256color", "COLORTERM=truecolor", "LANG=C", "LC_ALL=C"}
		if cmd.Path != "/fake/bash" || !reflect.DeepEqual(cmd.Args, args) || !reflect.DeepEqual(cmd.Env, env) || cmd.Dir != "/root" || cmd.WaitDelay != 100*time.Millisecond || cmd.Stdout != output || cmd.Stderr != output || cmd.Stdout != cmd.Stderr || cmd.Stdin != nil || cmd.Cancel == nil || cmd.SysProcAttr != nil || len(cmd.ExtraFiles) != 0 {
			t.Fatal("host command configuration changed")
		}
	}
}

func TestBashCapabilityFakeHostMetadata(t *testing.T) {
	for _, fail := range []bool{false, true} {
		probe := probeBashCapabilitiesWithRunner(nil, "/fake/bash", "", func(_ context.Context, w io.Writer) error {
			_, _ = io.WriteString(w, bashCapabilityFakeMarkers)
			if fail {
				return exec.ErrWaitDelay
			}
			return nil
		})
		metadata := SessionMetadata{Shell: "bash", Enhancement: "blesh"}
		applyHostBashCapabilities(&metadata, probe)
		c := metadata.Capabilities
		if !c.Resize || !c.ANSIColors || c.Readline == fail || c.BracketedPaste == fail || c.MultilinePaste == fail {
			t.Fatalf("metadata=%#v", metadata)
		}
		if (!fail && metadata.Enhancement != "blesh") || (fail && metadata.Enhancement != "native") {
			t.Fatalf("enhancement=%s", metadata.Enhancement)
		}
	}
}
