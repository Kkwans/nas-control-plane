package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
)

var errUnsupportedMediaType = errors.New("request requires application/json")

func validateJSONContentType(request *http.Request) error {
	values := request.Header.Values("Content-Type")
	if len(values) != 1 {
		return errUnsupportedMediaType
	}
	mediaType, _, err := mime.ParseMediaType(values[0])
	if err != nil || mediaType != "application/json" {
		return errUnsupportedMediaType
	}
	return nil
}

func (api *handler) writeUnsupportedMediaType(response http.ResponseWriter, request *http.Request) {
	api.writeError(response, request, http.StatusUnsupportedMediaType, "HTTP_UNSUPPORTED_MEDIA_TYPE", "请求必须使用 application/json 内容类型。")
}

func (api *handler) writeRequestBodyTooLarge(response http.ResponseWriter, request *http.Request) {
	api.writeError(response, request, http.StatusRequestEntityTooLarge, "HTTP_REQUEST_BODY_TOO_LARGE", "请求体超过大小限制。")
}

func isRequestBodyTooLarge(err error) bool {
	var limitError *http.MaxBytesError
	return errors.As(err, &limitError)
}

type guardedJSONBody struct {
	io.ReadCloser
	ctx        context.Context
	emptyReads int
}

func (body *guardedJSONBody) Read(p []byte) (int, error) {
	if err := body.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := body.ReadCloser.Read(p)
	if n > 0 {
		body.emptyReads = 0
	} else if err == nil {
		body.emptyReads++
		if body.emptyReads >= 100 {
			return 0, io.ErrNoProgress
		}
	}
	return n, err
}

// readBoundedJSONBody 先确认完整 body；最多向原 reader 读取 limit+1 字节。
// 取消检查依赖底层 HTTP body 配合，不能中断任意阻塞 Reader，也不接管 Close。
func readBoundedJSONBody(request *http.Request, limit int64) ([]byte, error) {
	body := request.Body
	if body == nil {
		body = http.NoBody
	}
	guarded := &guardedJSONBody{ReadCloser: body, ctx: request.Context()}
	data, err := io.ReadAll(http.MaxBytesReader(nil, guarded, limit))
	if err != nil {
		return nil, err
	}
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	return data, nil
}

type prefixedRequestBody struct {
	io.Reader
	io.Closer
}

// requestHasBody 只探测一个字节，并保留原始 Close；长度头不能证明实际空体。
func requestHasBody(request *http.Request) (bool, error) {
	if err := request.Context().Err(); err != nil {
		return false, err
	}
	if request.Body == nil || request.Body == http.NoBody {
		return false, nil
	}
	var first [1]byte
	// 容忍暂时的空读，但不让违反 Reader 进展约定的 body 无限阻塞。
	for attempts := 0; attempts < 100; attempts++ {
		if err := request.Context().Err(); err != nil {
			return false, err
		}
		n, err := request.Body.Read(first[:])
		if n > 0 {
			request.Body = &prefixedRequestBody{Reader: io.MultiReader(bytes.NewReader(first[:n]), request.Body), Closer: request.Body}
			if ctxErr := request.Context().Err(); ctxErr != nil {
				return true, ctxErr
			}
			if err != nil && !errors.Is(err, io.EOF) {
				return true, err
			}
			return true, nil
		}
		if ctxErr := request.Context().Err(); ctxErr != nil {
			return false, ctxErr
		}
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	return false, io.ErrNoProgress
}
