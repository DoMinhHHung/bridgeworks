package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

func NewRouter(
	serviceName string,
	logger *slog.Logger,
	readinessChecker ReadinessChecker,
	readinessTimeout time.Duration,
) http.Handler {
	router := chi.NewRouter()
	router.Use(RequestID)
	router.Use(Recoverer(logger))

	router.Get("/health/live", livenessHandler(serviceName))
	router.Get("/health/ready", readinessHandler(serviceName, readinessChecker, readinessTimeout))

	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, "not_found", "route not found", nil)
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	})

	return router
}
