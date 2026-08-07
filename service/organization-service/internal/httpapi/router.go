package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

type Dependencies struct {
	ServiceName               string
	Logger                    *slog.Logger
	Metrics                   Metrics
	Readiness                 ReadinessChecker
	ReadinessTimeout          time.Duration
	WebhookVerifier           WebhookVerifier
	WebhookProcessor          EventOutcomeProcessor
	WebhookMaxBytes           int64
	WebhookTimeout            time.Duration
	Authenticate              func(http.Handler) http.Handler
	CurrentResolver           CurrentOrganizationResolver
	Onboarding                OrganizationOnboarding
	BusinessEmailVerification BusinessEmailVerification
	MembershipAdministration  MembershipAdministration
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

	authenticated := func(handler http.HandlerFunc) http.Handler {
		return AuthenticatedResponseHeaders(dependencies.Authenticate(handler))
	}

	router.Method(http.MethodGet, "/organizations/current", authenticated(CurrentOrganization(dependencies.CurrentResolver)))
	router.Method(http.MethodPatch, "/organizations/current", authenticated(PatchCurrentOrganization(dependencies.CurrentResolver, dependencies.Onboarding)))
	router.Method(http.MethodPost, "/organizations/current/verification", authenticated(RequestCurrentOrganizationVerification(dependencies.CurrentResolver, dependencies.Onboarding)))
	router.Method(http.MethodPost, "/organizations/current/business-email-verification", authenticated(VerifyCurrentOrganizationBusinessEmail(dependencies.CurrentResolver, dependencies.BusinessEmailVerification)))
	router.Method(http.MethodPost, "/organizations/current/invitations", authenticated(CreateCurrentOrganizationInvitation(dependencies.CurrentResolver, dependencies.MembershipAdministration)))
	router.Method(http.MethodGet, "/organizations/current/membership", authenticated(CurrentMembership(dependencies.CurrentResolver)))
	router.Method(http.MethodDelete, "/organizations/current/membership", authenticated(LeaveCurrentOrganization(dependencies.CurrentResolver, dependencies.MembershipAdministration)))
	router.Method(http.MethodPatch, "/organizations/current/members/{membershipID}/role", authenticated(PatchCurrentOrganizationMemberRole(dependencies.CurrentResolver, dependencies.MembershipAdministration)))
	router.Method(http.MethodDelete, "/organizations/current/members/{membershipID}", authenticated(RemoveCurrentOrganizationMember(dependencies.CurrentResolver, dependencies.MembershipAdministration)))
	router.Method(http.MethodPost, "/organizations/current/ownership-transfer", authenticated(TransferCurrentOrganizationOwnership(dependencies.CurrentResolver, dependencies.MembershipAdministration)))

	return router
}
