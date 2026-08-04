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

func Liveness(serviceName string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, healthResponse{Status: "ok", Service: serviceName})
	}
}

func Readiness(serviceName string, checker ReadinessChecker, timeout time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		if err := checker.Check(ctx); err != nil {
			writeError(w, r, http.StatusServiceUnavailable, "service_unavailable", "service is not ready")
			return
		}
		writeJSON(w, http.StatusOK, healthResponse{Status: "ok", Service: serviceName})
	}
}
