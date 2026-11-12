package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

const realtimeWriteTimeout = 5 * time.Second

// prepareSSEStream 在提交 200 前验证流能力，不能为不支持 deadline 的 writer 放宽预算。
func (api *handler) prepareSSEStream(ctx context.Context, response http.ResponseWriter) (*http.ResponseController, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !supportsSSEFlush(response) {
		return nil, http.ErrNotSupported
	}
	controller := http.NewResponseController(response)
	if err := controller.SetWriteDeadline(api.currentTime().Add(realtimeWriteTimeout)); err != nil {
		return nil, err
	}
	if err := controller.SetWriteDeadline(time.Time{}); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return controller, nil
}

// 包装器可能无条件实现 FlushError；穿透 Unwrap，避免底层不支持时才提交 200。
func supportsSSEFlush(response http.ResponseWriter) bool {
	for {
		if wrapper, ok := response.(interface{ Unwrap() http.ResponseWriter }); ok {
			response = wrapper.Unwrap()
			continue
		}
		switch response.(type) {
		case interface{ FlushError() error }, http.Flusher:
			return true
		default:
			return false
		}
	}
}

// 每帧以同一预算覆盖 Write 和 Flush；清除 deadline 后可安全等待长间隔的下次采样。
// 取消后不再准入新的写入；已经开始的 I/O 仍由最多 5 秒的 deadline 中断。
func (api *handler) writeSSEFrame(ctx context.Context, response http.ResponseWriter, controller *http.ResponseController, frame string) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = controller.SetWriteDeadline(api.currentTime().Add(realtimeWriteTimeout)); err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, controller.SetWriteDeadline(time.Time{}))
	}()
	if err = ctx.Err(); err != nil {
		return err
	}
	written, err := io.WriteString(response, frame)
	if err != nil {
		return err
	}
	if written != len(frame) {
		return io.ErrShortWrite
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = controller.Flush(); err != nil {
		return err
	}
	return ctx.Err()
}
