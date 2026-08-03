package httpapi

import (
	"context"
	"net/http"
	"time"
)

type ReadinessChecker interface {
	Check(context.Context) error
}

type healthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

func livenessHandler(serviceName string) http.HandlerFunc {
	response := healthResponse{
		Status:  "ok",
		Service: serviceName,
	}

	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, response)
	}
}

func readinessHandler(serviceName string, checker ReadinessChecker, timeout time.Duration) http.HandlerFunc {
	response := healthResponse{
		Status:  "ok",
		Service: serviceName,
	}

	return func(w http.ResponseWriter, r *http.Request) {
		checkContext, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		if err := checker.Check(checkContext); err != nil {
			writeError(
				w,
				r,
				http.StatusServiceUnavailable,
				"service_unavailable",
				"service is not ready",
				nil,
			)
			return
		}

		writeJSON(w, http.StatusOK, response)
	}
}
