package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func NewRouter(serviceName string, logger *slog.Logger) http.Handler {
	router := chi.NewRouter()
	router.Use(RequestID)
	router.Use(Recoverer(logger))

	router.Get("/health/live", healthHandler(serviceName))
	router.Get("/health/ready", healthHandler(serviceName))

	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, "not_found", "route not found", nil)
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	})

	return router
}
