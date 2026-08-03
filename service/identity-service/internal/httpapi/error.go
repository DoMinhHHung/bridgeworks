package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
)

type errorEnvelope struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Details   any    `json:"details"`
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, details any) {
	writeJSON(w, status, errorEnvelope{
		Code:      code,
		Message:   message,
		RequestID: RequestIDFromContext(r.Context()),
		Details:   details,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	var buffer bytes.Buffer
	if err := json.NewEncoder(&buffer).Encode(body); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buffer.Bytes())
}
