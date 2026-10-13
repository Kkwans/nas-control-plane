package terminal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// 仅用内存 Reader 与 channel 模拟 attachment，不创建 socket、进程或 PTY。
type wireFakeConn struct {
	net.Conn
	reader  io.Reader
	closeFn func() error
	reads   atomic.Int32
	closes  atomic.Int32
}

func (c *wireFakeConn) Read(p []byte) (int, error) {
	c.reads.Add(1)
	return c.reader.Read(p)
}

func (c *wireFakeConn) Close() error {
	c.closes.Add(1)
	if c.closeFn != nil {
		return c.closeFn()
	}
	return nil
}

type wireFakeReadFunc func([]byte) (int, error)

func (f wireFakeReadFunc) Read(p []byte) (int, error) { return f(p) }

type wireFakeChunkReader struct {
	data     []byte
	chunk    int
	position int
	sawEOF   bool
}

func (r *wireFakeChunkReader) Read(p []byte) (int, error) {
	if r.position == len(r.data) {
		r.sawEOF = true
		return 0, io.EOF
	}
	if r.chunk > 0 && len(p) > r.chunk {
		p = p[:r.chunk]
	}
	n := copy(p, r.data[r.position:])
	r.position += n
	return n, nil
}

func wireFakeFrame(stream byte, payload string) []byte {
	frame := make([]byte, 8+len(payload))
	frame[0] = stream
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[8:], payload)
	return frame
}

func wireFakeAssertClosedOnce(t *testing.T, conn *wireFakeConn) {
	t.Helper()
	if got := conn.closes.Load(); got != 1 {
		t.Fatalf("attachment Close calls = %d, want 1", got)
	}
}

func wireFakeAssertFailure(t *testing.T, output []byte, err error, causes ...error) {
	t.Helper()
	if output != nil || err == nil {
		t.Fatalf("failure exposed output %q or missing error: %v", output, err)
	}
	for _, cause := range causes {
		if !errors.Is(err, cause) {
			t.Errorf("error %v does not preserve cause %v", err, cause)
		}
	}
}

func TestContainerProbeReadWireBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		size  int
		chunk int
	}{
		{name: "empty", size: 0},
		{name: "below_limit", size: maxCapabilityProbeOutput - 1},
		{name: "exact_limit", size: maxCapabilityProbeOutput},
		{name: "sentinel_byte", size: maxCapabilityProbeOutput + 1},
		{name: "overflow_multiple_chunks", size: maxCapabilityProbeOutput + 4096, chunk: 37},
		{name: "below_limit_multiple_chunks", size: maxCapabilityProbeOutput - 1, chunk: 19},
		{name: "exact_limit_multiple_chunks", size: maxCapabilityProbeOutput, chunk: 29},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := bytes.Repeat([]byte{'x'}, tc.size)
			if len(data) >= 2*len("private-marker") {
				copy(data, "private-marker")
				copy(data[len(data)-len("private-marker"):], "private-marker")
			}
			reader := &wireFakeChunkReader{data: data, chunk: tc.chunk}
			conn := &wireFakeConn{reader: reader}
			output, err := readContainerProbe(context.Background(), conn, conn)
			wireFakeAssertClosedOnce(t, conn)
			if tc.size > maxCapabilityProbeOutput {
				wireFakeAssertFailure(t, output, err, errContainerProbeOutputLimit)
				if reader.position != maxCapabilityProbeOutput+1 || reader.sawEOF {
					t.Fatalf("overflow read %d bytes, EOF = %t; want one sentinel byte and no drain", reader.position, reader.sawEOF)
				}
				if strings.Contains(err.Error(), "private-marker") {
					t.Fatalf("error includes captured output: %v", err)
				}
				return
			}
			if err != nil || !bytes.Equal(output, data) {
				t.Fatalf("read = %d bytes, %v; want %d bytes unchanged", len(output), err, len(data))
			}
			if !reader.sawEOF {
				t.Fatal("success without observing the transport EOF")
			}
		})
	}
}

func TestContainerProbeReadPrefetchedAttachmentReader(t *testing.T) {
	wire := wireFakeFrame(1, "__NCP_READLINE__=1\n")
	conn := &wireFakeConn{reader: bytes.NewReader(wire)}
	reader := bufio.NewReader(conn)
	if _, err := reader.Peek(1); err != nil {
		t.Fatal(err)
	}
	if reader.Buffered() != len(wire) {
		t.Fatalf("fixture did not prefetch wire: buffered %d, want %d", reader.Buffered(), len(wire))
	}
	output, err := readContainerProbe(context.Background(), reader, conn)
	if err != nil || !bytes.Equal(output, wire) {
		t.Fatalf("prefetched attachment bytes lost: output %q, error %v", output, err)
	}
	wireFakeAssertClosedOnce(t, conn)
}

func TestContainerProbeReadFrameOverheadCountsTowardLimit(t *testing.T) {
	wire := append(wireFakeFrame(1, "marker"), bytes.Repeat(wireFakeFrame(2, ""), maxCapabilityProbeOutput/8+1)...)
	reader := &wireFakeChunkReader{data: wire, chunk: 13}
	conn := &wireFakeConn{reader: reader}
	output, err := readContainerProbe(context.Background(), conn, conn)
	wireFakeAssertFailure(t, output, err, errContainerProbeOutputLimit)
	if reader.position != maxCapabilityProbeOutput+1 {
		t.Fatalf("read %d wire bytes, want %d", reader.position, maxCapabilityProbeOutput+1)
	}
	wireFakeAssertClosedOnce(t, conn)
}

func TestContainerProbeReadKeepsReadAndCloseErrors(t *testing.T) {
	readErr := errors.New("wire read failed")
	closeErr := errors.New("wire close failed")
	for _, tc := range []struct {
		name     string
		data     []byte
		readErr  error
		closeErr error
	}{
		{name: "read_error_before_data", readErr: readErr},
		{name: "data_and_read_error", data: []byte("private-marker"), readErr: readErr},
		{name: "unexpected_EOF_is_read_error", data: []byte("private-marker"), readErr: io.ErrUnexpectedEOF},
		{name: "read_and_close_errors", data: []byte("private-marker"), readErr: readErr, closeErr: closeErr},
		{name: "successful_read_close_error", data: []byte("private-marker"), readErr: io.EOF, closeErr: closeErr},
		{name: "sentinel_read_and_close_errors", data: bytes.Repeat([]byte{'x'}, maxCapabilityProbeOutput+1), readErr: readErr, closeErr: closeErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			position := 0
			conn := &wireFakeConn{reader: wireFakeReadFunc(func(p []byte) (int, error) {
				n := copy(p, tc.data[position:])
				position += n
				if position == len(tc.data) {
					return n, tc.readErr
				}
				return n, nil
			}), closeFn: func() error { return tc.closeErr }}
			output, err := readContainerProbe(context.Background(), conn, conn)
			causes := []error{}
			if tc.readErr != io.EOF {
				causes = append(causes, tc.readErr)
			}
			if tc.closeErr != nil {
				causes = append(causes, tc.closeErr)
			}
			if len(tc.data) > maxCapabilityProbeOutput {
				causes = append(causes, errContainerProbeOutputLimit)
			}
			wireFakeAssertFailure(t, output, err, causes...)
			if strings.Contains(err.Error(), "private-marker") {
				t.Fatalf("error includes captured output: %v", err)
			}
			wireFakeAssertClosedOnce(t, conn)
		})
	}
}

func TestContainerProbeReadErrorAfterMarker(t *testing.T) {
	readErr := errors.New("read failed after marker")
	data := []byte("__NCP_READLINE__=1\n")
	reader := io.MultiReader(bytes.NewReader(data), wireFakeReadFunc(func([]byte) (int, error) { return 0, readErr }))
	conn := &wireFakeConn{reader: reader}
	output, err := readContainerProbe(context.Background(), conn, conn)
	wireFakeAssertFailure(t, output, err, readErr)
	wireFakeAssertClosedOnce(t, conn)
}

func TestContainerProbeReadDataWithEOF(t *testing.T) {
	for _, size := range []int{0, 17, maxCapabilityProbeOutput, maxCapabilityProbeOutput + 1} {
		data := bytes.Repeat([]byte{'x'}, size)
		position := 0
		conn := &wireFakeConn{reader: wireFakeReadFunc(func(p []byte) (int, error) {
			n := copy(p, data[position:])
			position += n
			if position == len(data) {
				return n, io.EOF
			}
			return n, nil
		})}
		output, err := readContainerProbe(context.Background(), conn, conn)
		if size > maxCapabilityProbeOutput {
			wireFakeAssertFailure(t, output, err, errContainerProbeOutputLimit)
		} else if err != nil || !bytes.Equal(output, data) {
			t.Fatalf("%d data bytes with natural EOF = %d bytes, %v", size, len(output), err)
		}
		wireFakeAssertClosedOnce(t, conn)
	}
}

func TestContainerProbeReadJoinsCancelOverflowReadAndClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readErr := errors.New("sentinel read failed")
	closeErr := errors.New("sentinel close failed")
	data := bytes.Repeat([]byte{'x'}, maxCapabilityProbeOutput+1)
	position := 0
	conn := &wireFakeConn{reader: wireFakeReadFunc(func(p []byte) (int, error) {
		n := copy(p, data[position:])
		position += n
		if position == len(data) {
			cancel()
			return n, readErr
		}
		return n, nil
	}), closeFn: func() error { return closeErr }}
	output, err := readContainerProbe(ctx, conn, conn)
	wireFakeAssertFailure(t, output, err, context.Canceled, errContainerProbeOutputLimit, readErr, closeErr)
	wireFakeAssertClosedOnce(t, conn)
}

func TestContainerProbeReadCanceledBufferedMarkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn := &wireFakeConn{reader: strings.NewReader("__NCP_READLINE__=1\n")}
	output, err := readContainerProbe(ctx, conn, conn)
	wireFakeAssertFailure(t, output, err, context.Canceled)
	if conn.reads.Load() != 0 {
		t.Fatal("read prefetched marker despite already canceled context")
	}
	wireFakeAssertClosedOnce(t, conn)
}

func TestContainerProbeReadCancellationDiscardsLateDataAndEOF(t *testing.T) {
	for _, tc := range []struct {
		name    string
		data    string
		readErr error
	}{
		{name: "buffered_marker", data: "__NCP_READLINE__=1\n"},
		{name: "EOF_race", readErr: io.EOF},
		{name: "data_with_EOF_race", data: "__NCP_READLINE__=1\n", readErr: io.EOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn := &wireFakeConn{reader: wireFakeReadFunc(func(p []byte) (int, error) {
				cancel()
				return copy(p, tc.data), tc.readErr
			})}
			output, err := readContainerProbe(ctx, conn, conn)
			wireFakeAssertFailure(t, output, err, context.Canceled)
			wireFakeAssertClosedOnce(t, conn)
		})
	}
}

func TestContainerProbeReadCancellationDuringClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn := &wireFakeConn{reader: strings.NewReader("marker"), closeFn: func() error { cancel(); return nil }}
	output, err := readContainerProbe(ctx, conn, conn)
	wireFakeAssertFailure(t, output, err, context.Canceled)
	wireFakeAssertClosedOnce(t, conn)
}

func TestContainerProbeReadExactLimitWaitsForEOFOrCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		blocked := make(chan struct{})
		closed := make(chan struct{})
		reader := io.MultiReader(bytes.NewReader(bytes.Repeat([]byte{'x'}, maxCapabilityProbeOutput)), wireFakeReadFunc(func([]byte) (int, error) {
			close(blocked)
			<-closed
			return 0, net.ErrClosed
		}))
		conn := &wireFakeConn{reader: reader, closeFn: func() error { close(closed); return nil }}
		type result struct {
			output []byte
			err    error
		}
		done := make(chan result, 1)
		go func() { output, err := readContainerProbe(ctx, conn, conn); done <- result{output, err} }()
		synctest.Wait()
		select {
		case result := <-done:
			t.Fatalf("exact limit falsely completed before EOF: output %d bytes, error %v", len(result.output), result.err)
		default:
		}
		select {
		case <-blocked:
		default:
			t.Fatal("reader did not try to prove EOF after the exact wire limit")
		}
		cancel()
		got := <-done
		wireFakeAssertFailure(t, got.output, got.err, context.Canceled, net.ErrClosed)
		wireFakeAssertClosedOnce(t, conn)
	})
}

func TestContainerProbeReadDeadlineClosesSameConn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		closed := make(chan struct{})
		conn := &wireFakeConn{reader: wireFakeReadFunc(func([]byte) (int, error) {
			<-closed
			return 0, net.ErrClosed
		}), closeFn: func() error { close(closed); return nil }}
		output, err := readContainerProbe(ctx, conn, conn)
		wireFakeAssertFailure(t, output, err, context.DeadlineExceeded, net.ErrClosed)
		wireFakeAssertClosedOnce(t, conn)
	})
}

func TestContainerProbeReadCancellationJoinsCloseCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		readStarted := make(chan struct{})
		closeStarted := make(chan struct{})
		readerReleased := make(chan struct{})
		closeReleased := make(chan struct{})
		readErr := errors.New("canceled transport read")
		closeErr := errors.New("canceled transport close")
		conn := &wireFakeConn{reader: wireFakeReadFunc(func([]byte) (int, error) {
			close(readStarted)
			<-readerReleased
			return 0, readErr
		}), closeFn: func() error {
			close(closeStarted)
			close(readerReleased)
			<-closeReleased
			return closeErr
		}}
		type result struct {
			output []byte
			err    error
		}
		done := make(chan result, 1)
		go func() { output, err := readContainerProbe(ctx, conn, conn); done <- result{output, err} }()
		<-readStarted
		cancel()
		<-closeStarted
		synctest.Wait()
		select {
		case result := <-done:
			close(closeReleased)
			t.Fatalf("read owner returned before its cancel callback finished Close: output %q, error %v", result.output, result.err)
		default:
		}
		close(closeReleased)
		got := <-done
		wireFakeAssertFailure(t, got.output, got.err, context.Canceled, readErr, closeErr)
		wireFakeAssertClosedOnce(t, conn)
	})
}

func TestContainerProbeDecodeCompleteFrames(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire []byte
		want string
	}{
		{name: "empty"},
		{name: "stdout", wire: wireFakeFrame(1, "stdout"), want: "stdout"},
		{name: "stderr", wire: wireFakeFrame(2, "stderr"), want: "stderr"},
		{name: "legacy_stdin", wire: wireFakeFrame(0, "legacy"), want: "legacy"},
		{name: "zero_length_frames", wire: append(append(wireFakeFrame(0, ""), wireFakeFrame(1, "")...), wireFakeFrame(2, "")...)},
		{name: "wire_order", wire: append(append(wireFakeFrame(2, "err"), wireFakeFrame(1, "out")...), wireFakeFrame(0, "legacy")...), want: "erroutlegacy"},
		{name: "maximum_wire", wire: wireFakeFrame(1, strings.Repeat("x", maxCapabilityProbeOutput-8)), want: strings.Repeat("x", maxCapabilityProbeOutput-8)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, err := decodeContainerProbeOutput(tc.wire)
			if err != nil || string(output) != tc.want {
				t.Fatalf("decoded %q, %v; want %q", output, err, tc.want)
			}
			if cap(output) > len(tc.wire) {
				t.Fatalf("decoded allocation capacity %d exceeds captured wire %d", cap(output), len(tc.wire))
			}
		})
	}
}

func TestContainerProbeDecodeMarkersAcrossFramesAndReaderChunks(t *testing.T) {
	wire := append(wireFakeFrame(1, "__NCP_READ"), wireFakeFrame(2, "LINE__=1\n")...)
	wire = append(wire, wireFakeFrame(0, "__NCP_PASTE__=1\n")...)
	for _, chunk := range []int{1, 3, 7, 8, 17} {
		reader := &wireFakeChunkReader{data: append([]byte(nil), wire...), chunk: chunk}
		conn := &wireFakeConn{reader: reader}
		raw, err := readContainerProbe(context.Background(), conn, conn)
		if err != nil {
			t.Fatalf("chunk %d: read error %v", chunk, err)
		}
		output, err := decodeContainerProbeOutput(raw)
		if err != nil || string(output) != "__NCP_READLINE__=1\n__NCP_PASTE__=1\n" {
			t.Fatalf("chunk %d: decoded %q, error %v", chunk, output, err)
		}
		wireFakeAssertClosedOnce(t, conn)
	}
}

func TestContainerProbeDecodeRejectsMalformedFramesWithoutPrefix(t *testing.T) {
	maxLength := make([]byte, 8)
	maxLength[0] = 1
	binary.BigEndian.PutUint32(maxLength[4:], ^uint32(0))
	cases := []struct {
		name string
		wire []byte
	}{
		{name: "raw_marker_no_fallback", wire: []byte("__NCP_READLINE__=1\n")},
		{name: "missing_body", wire: wireFakeFrame(1, "x")[:8]},
		{name: "partial_body", wire: wireFakeFrame(1, "body")[:11]},
		{name: "maximum_uint32_length", wire: maxLength},
		{name: "system_error", wire: wireFakeFrame(3, "private-daemon-error")},
		{name: "unknown_stream", wire: wireFakeFrame(4, "payload")},
		{name: "maximum_stream", wire: wireFakeFrame(255, "payload")},
		{name: "zero_length_system_error", wire: wireFakeFrame(3, "")},
	}
	for n := 1; n < 8; n++ {
		cases = append(cases, struct {
			name string
			wire []byte
		}{name: "partial_header_" + string(rune('0'+n)), wire: wireFakeFrame(1, "")[:n]})
	}
	for index := 1; index <= 3; index++ {
		wire := wireFakeFrame(1, "payload")
		wire[index] = 1
		cases = append(cases, struct {
			name string
			wire []byte
		}{name: "reserved_byte_" + string(rune('0'+index)), wire: wire})
	}
	for _, tc := range cases {
		for _, prefix := range []bool{false, true} {
			name := tc.name
			wire := append([]byte(nil), tc.wire...)
			if prefix {
				name += "_after_valid_marker"
				wire = append(wireFakeFrame(1, "private-marker\n"), wire...)
			}
			t.Run(name, func(t *testing.T) {
				output, err := decodeContainerProbeOutput(wire)
				wireFakeAssertFailure(t, output, err)
				if strings.Contains(err.Error(), "private-marker") || strings.Contains(err.Error(), "private-daemon-error") {
					t.Fatalf("decoder error includes captured output: %v", err)
				}
			})
		}
	}
}

func TestContainerProbeReadRejectsRepeatedEmptyReads(t *testing.T) {
	reads := 0
	conn := &wireFakeConn{reader: wireFakeReadFunc(func([]byte) (int, error) {
		reads++
		if reads > 100 {
			return 0, errors.New("test safety stop after excessive empty reads")
		}
		return 0, nil
	})}
	output, err := readContainerProbe(context.Background(), conn, conn)
	wireFakeAssertFailure(t, output, err, io.ErrNoProgress)
	if reads > 100 {
		t.Fatalf("empty reader was not bounded: %d read calls", reads)
	}
	wireFakeAssertClosedOnce(t, conn)
}

func TestContainerProbeReadResetsEmptyReadCountAfterProgress(t *testing.T) {
	reads := 0
	conn := &wireFakeConn{reader: wireFakeReadFunc(func(p []byte) (int, error) {
		reads++
		switch reads {
		case 100:
			p[0] = 'x'
			return 1, nil
		case 200:
			return 0, io.EOF
		default:
			return 0, nil
		}
	})}
	output, err := readContainerProbe(context.Background(), conn, conn)
	if err != nil || string(output) != "x" {
		t.Fatalf("separate empty-read bursts hid intervening progress: output %q, error %v", output, err)
	}
	wireFakeAssertClosedOnce(t, conn)
}

func TestContainerProbeDecodeRejectsOversizedWireInput(t *testing.T) {
	wire := wireFakeFrame(1, strings.Repeat("x", maxCapabilityProbeOutput-7))
	output, err := decodeContainerProbeOutput(wire)
	wireFakeAssertFailure(t, output, err, errContainerProbeOutputLimit)
}
