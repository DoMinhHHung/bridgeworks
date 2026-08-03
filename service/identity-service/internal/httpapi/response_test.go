package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSONEncodingFailureUsesSafeErrorEnvelope(t *testing.T) {
	const requestID = "encoding-failure-request"

	response := httptest.NewRecorder()
	response.Header().Set(RequestIDHeader, requestID)

	writeJSON(response, http.StatusOK, map[string]any{
		"unsupported": make(chan struct{}),
	})

	assertErrorResponse(
		t,
		response,
		http.StatusInternalServerError,
		"internal_error",
		"internal server error",
		requestID,
	)

	responseBody := response.Body.String()
	for _, forbidden := range []string{"unsupported type", "chan struct", "json:"} {
		if strings.Contains(responseBody, forbidden) {
			t.Fatalf("response leaks %q: %s", forbidden, responseBody)
		}
	}
}
