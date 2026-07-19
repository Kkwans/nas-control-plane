package agentsocket

import "errors"

type codedError struct {
	code string
	err  error
}

func coded(code string, err error) error {
	return &codedError{code: code, err: err}
}

func (e *codedError) Error() string {
	return e.code
}

func (e *codedError) Unwrap() error {
	return e.err
}

func ErrorCode(err error) string {
	var coded *codedError
	if errors.As(err, &coded) {
		return coded.code
	}
	return ""
}
