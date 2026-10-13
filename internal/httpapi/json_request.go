package httpapi

import (
	"bytes"
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

type prefixedRequestBody struct {
	io.Reader
	io.Closer
}

// requestHasBody 只探测一个字节，并保留原始 Close；长度头不能证明实际空体。
func requestHasBody(request *http.Request) (bool, error) {
	if request.Body == nil || request.Body == http.NoBody {
		return false, nil
	}
	var first [1]byte
	// 容忍暂时的空读，但不让违反 Reader 进展约定的 body 无限阻塞。
	for attempts := 0; attempts < 100; attempts++ {
		n, err := request.Body.Read(first[:])
		if n > 0 {
			request.Body = &prefixedRequestBody{Reader: io.MultiReader(bytes.NewReader(first[:n]), request.Body), Closer: request.Body}
			if err != nil && !errors.Is(err, io.EOF) {
				return true, err
			}
			return true, nil
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
