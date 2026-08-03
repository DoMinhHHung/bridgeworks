package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func NewRouter(serviceName string, logger *slog.Logger) http.Handler {
	router := chi.NewRouter()
	router.Use(RequestID)
	router.Use(Recover(logger))
	router.Use(AccessLog(logger))

	router.Get("/health/live", healthHandler(serviceName))
	router.Get("/health/ready", healthHandler(serviceName))

	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, "not_found", "resource not found")
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	})

	return router
}

func healthHandler(serviceName string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, healthResponse{
			Status:  "ok",
			Service: serviceName,
		})
	}
}
