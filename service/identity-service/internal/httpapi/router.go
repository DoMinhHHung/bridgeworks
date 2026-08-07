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
	Metrics                    Metrics
}

func NewRouter(
	config RouterConfig,
	logger *slog.Logger,
	readinessChecker ReadinessChecker,
	clerkVerifier ClerkWebhookVerifier,
	clerkProcessor ClerkWebhookOutcomeProcessor,
	authenticate func(http.Handler) http.Handler,
	currentUserGetter CurrentUserGetter,
	platformAccessResolver PlatformAccessResolver,
) http.Handler {
	if authenticate == nil {
		authenticate = func(next http.Handler) http.Handler { return next }
	}

	router := chi.NewRouter()
	router.Use(RequestID)
	router.Use(HTTPObservability(logger, config.Metrics))
	router.Use(Recoverer(logger))

	router.Get("/health/live", livenessHandler(config.ServiceName))
	router.Get("/health/ready", readinessHandler(config.ServiceName, readinessChecker, config.ReadinessTimeout))
	router.Post(
		"/webhooks/clerk",
		clerkWebhookHandlerWithMetrics(
			logger,
			clerkVerifier,
			clerkProcessor,
			config.Metrics,
			config.ClerkWebhookMaxBodyBytes,
			config.ClerkWebhookProcessTimeout,
		),
	)
	router.With(currentUserResponseHeaders, authenticate).Get("/me", currentUserHandler(logger, currentUserGetter))
	router.With(platformAccessResponseHeaders, authenticate).Get(
		"/internal/v1/platform-access/me",
		platformAccessHandler(logger, platformAccessResolver),
	)

	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, "not_found", "route not found", nil)
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	})
	return router
}
