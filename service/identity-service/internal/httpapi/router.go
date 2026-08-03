package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

type RouterConfig struct {
	ServiceName                string
	ReadinessTimeout           time.Duration
	ClerkWebhookProcessTimeout time.Duration
	ClerkWebhookMaxBodyBytes   int64
}

func NewRouter(
	config RouterConfig,
	logger *slog.Logger,
	readinessChecker ReadinessChecker,
	clerkVerifier ClerkWebhookVerifier,
	clerkProcessor ClerkWebhookProcessor,
) http.Handler {
	router := chi.NewRouter()
	router.Use(RequestID)
	router.Use(Recoverer(logger))

	router.Get("/health/live", livenessHandler(config.ServiceName))
	router.Get("/health/ready", readinessHandler(config.ServiceName, readinessChecker, config.ReadinessTimeout))
	router.Post(
		"/webhooks/clerk",
		clerkWebhookHandler(
			logger,
			clerkVerifier,
			clerkProcessor,
			config.ClerkWebhookMaxBodyBytes,
			config.ClerkWebhookProcessTimeout,
		),
	)

	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, "not_found", "route not found", nil)
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	})
	return router
}
