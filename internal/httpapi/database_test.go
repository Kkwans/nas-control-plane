package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
)

func TestWriteDatabaseErrorPreservesStableCodeWithoutSensitiveMessage(t *testing.T) {
	tests := []struct {
		code   ncpdatabase.ErrorCode
		status int
	}{
		{code: ncpdatabase.CodeCredentialsRequired, status: http.StatusBadRequest},
		{code: ncpdatabase.CodeAuthFailed, status: http.StatusUnauthorized},
		{code: ncpdatabase.CodeDatabaseNotFound, status: http.StatusNotFound},
		{code: ncpdatabase.CodePermissionDenied, status: http.StatusForbidden},
		{code: ncpdatabase.CodeConstraintFailed, status: http.StatusConflict},
		{code: ncpdatabase.CodeTimeout, status: http.StatusGatewayTimeout},
	}
	for _, testCase := range tests {
		t.Run(string(testCase.code), func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/query", nil)
			err := &ncpdatabase.DatabaseError{Code: testCase.code, Cause: errors.New("password=hidden")}
			(&handler{}).writeDatabaseError(response, request, err)
			if response.Code != testCase.status {
				t.Fatalf("status = %d, want %d", response.Code, testCase.status)
			}
			if !strings.Contains(response.Body.String(), string(testCase.code)) || strings.Contains(response.Body.String(), "hidden") {
				t.Fatalf("response leaked or lost stable code: %s", response.Body.String())
			}
		})
	}
}
