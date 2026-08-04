package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

type Dependencies struct {
	ServiceName      string
	Logger           *slog.Logger
	Metrics          Metrics
	Readiness        ReadinessChecker
	ReadinessTimeout time.Duration
	WebhookVerifier  WebhookVerifier
	WebhookProcessor EventProcessor
	WebhookMaxBytes  int64
	WebhookTimeout   time.Duration
	Authenticate     func(http.Handler) http.Handler
	CurrentResolver  CurrentOrganizationResolver
}

func NewRouter(dependencies Dependencies) http.Handler {
	router := chi.NewRouter()
	router.Use(RequestID)
	router.Use(HTTPObservability(dependencies.Logger, dependencies.Metrics))
	router.Use(Recoverer(dependencies.Logger))

	router.Get("/health/live", Liveness(dependencies.ServiceName))
	router.Get("/health/ready", Readiness(dependencies.ServiceName, dependencies.Readiness, dependencies.ReadinessTimeout))
	router.Post("/webhooks/clerk", ClerkWebhookWithMetrics(
		dependencies.WebhookVerifier,
		dependencies.WebhookProcessor,
		dependencies.Metrics,
		dependencies.Logger,
		dependencies.WebhookMaxBytes,
		dependencies.WebhookTimeout,
	))

	currentOrganization := AuthenticatedResponseHeaders(
		dependencies.Authenticate(CurrentOrganization(dependencies.CurrentResolver)),
	)
	currentMembership := AuthenticatedResponseHeaders(
		dependencies.Authenticate(CurrentMembership(dependencies.CurrentResolver)),
	)
	router.Method(http.MethodGet, "/organizations/current", currentOrganization)
	router.Method(http.MethodGet, "/organizations/current/membership", currentMembership)

	return router
}
