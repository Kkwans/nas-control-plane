package httpapi

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

type requestIDContextKey struct{}
type requestMetadataContextKey struct{}

type requestMetadata struct {
	actor     string
	errorCode string
}

func requestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

func requestMetadataFromContext(ctx context.Context) *requestMetadata {
	metadata, _ := ctx.Value(requestMetadataContextKey{}).(*requestMetadata)
	return metadata
}

var requestIDSequence atomic.Uint64

func defaultRequestID() string {
	return fmt.Sprintf("req-%d", requestIDSequence.Add(1))
}

// withRequestID adds the request correlation id and emits one structured
// completion record. It deliberately records only route and operation
// metadata; query strings and request bodies can contain credentials or SQL
// and are never included in logs.
func (api *handler) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestID := api.newRequestID()
		if requestID == "" {
			requestID = defaultRequestID()
		}
		metadata := &requestMetadata{actor: "anonymous"}
		contextWithMetadata := context.WithValue(request.Context(), requestIDContextKey{}, requestID)
		contextWithMetadata = context.WithValue(contextWithMetadata, requestMetadataContextKey{}, metadata)
		response.Header().Set("X-Request-ID", requestID)
		recorder := &responseRecorder{ResponseWriter: response}
		startedAt := time.Now()
		next.ServeHTTP(recorder, request.WithContext(contextWithMetadata))
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		level := slog.LevelInfo
		if status >= http.StatusInternalServerError {
			level = slog.LevelError
		} else if status >= http.StatusBadRequest {
			level = slog.LevelWarn
		}
		attrs := []slog.Attr{
			slog.String("component", "httpapi"),
			slog.String("request_id", requestID),
			slog.String("actor", metadata.actor),
			slog.String("action", request.Method),
			slog.String("route", request.URL.Path),
			slog.Int("status", status),
			slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
		}
		if metadata.errorCode != "" {
			attrs = append(attrs, slog.String("error_code", metadata.errorCode))
		}
		api.logger.LogAttrs(request.Context(), level, "http_request", attrs...)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (recorder *responseRecorder) WriteHeader(status int) {
	if recorder.wroteHeader {
		return
	}
	recorder.wroteHeader = true
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *responseRecorder) Write(payload []byte) (int, error) {
	if !recorder.wroteHeader {
		recorder.WriteHeader(http.StatusOK)
	}
	return recorder.ResponseWriter.Write(payload)
}

func (recorder *responseRecorder) Unwrap() http.ResponseWriter {
	return recorder.ResponseWriter
}

func (recorder *responseRecorder) Flush() {
	if !recorder.wroteHeader {
		recorder.WriteHeader(http.StatusOK)
	}
	if flusher, ok := recorder.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (recorder *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := recorder.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (recorder *responseRecorder) Push(target string, options *http.PushOptions) error {
	pusher, ok := recorder.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, options)
}

func (recorder *responseRecorder) ReadFrom(reader io.Reader) (int64, error) {
	if !recorder.wroteHeader {
		recorder.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := recorder.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(reader)
	}
	return io.Copy(recorder.ResponseWriter, reader)
}
