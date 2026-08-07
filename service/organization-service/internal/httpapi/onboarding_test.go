package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authn"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationonboarding"
	"github.com/google/uuid"
)

type onboardingResolverStub struct {
	result currentorganization.Result
	err    error
}

func (s onboardingResolverStub) Resolve(context.Context, authorization.Principal, string, string) (currentorganization.Result, error) {
	return s.result, s.err
}

type onboardingStub struct {
	profilePatch organizationonboarding.ProfilePatch
	profileActor authorization.ActorContext
	profile      currentorganization.Organization
	profileErr   error
	verifyActor  authorization.ActorContext
	verification currentorganization.Organization
	verifyErr    error
}

func (s *onboardingStub) UpdateProfile(_ context.Context, actor authorization.ActorContext, patch organizationonboarding.ProfilePatch) (currentorganization.Organization, error) {
	s.profileActor = actor
	s.profilePatch = patch
	return s.profile, s.profileErr
}

func (s *onboardingStub) RequestVerification(_ context.Context, actor authorization.ActorContext) (currentorganization.Organization, error) {
	s.verifyActor = actor
	return s.verification, s.verifyErr
}

func TestPatchCurrentOrganizationUsesResolvedLocalActor(t *testing.T) {
	organizationID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000201")
	membershipID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000202")
	identityID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000203")
	actor := authorization.NewActorContext(
		identityID,
		organizationID,
		membershipID,
		"admin",
		[]string{authorization.PermissionOrganizationManage},
	)
	resolver := onboardingResolverStub{result: currentorganization.Result{Actor: actor}}
	legalName := "BridgeWorks Ltd"
	country := "VN"
	stub := &onboardingStub{profile: currentorganization.Organization{
		ID: organizationID, Status: "active", LegalName: &legalName, Country: &country,
		VerificationStatus: "unverified", TrustStatus: "unassessed",
	}}

	request := httptest.NewRequest(http.MethodPatch, "/organizations/current", strings.NewReader("{\"legal_name\":\" BridgeWorks Ltd \",\"country\":\"vn\"}"))
	request = request.WithContext(authn.ContextWithPrincipal(request.Context(), authorization.Principal{
		ClerkUserID: "verified-user", SessionID: "verified-session", ClerkOrganizationID: "verified-org",
	}))
	recorder := httptest.NewRecorder()
	PatchCurrentOrganization(resolver, stub).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if stub.profileActor.OrganizationID != organizationID || stub.profileActor.Role != "admin" {
		t.Fatalf("actor = %#v", stub.profileActor)
	}
	if !stub.profilePatch.LegalName.Set || stub.profilePatch.LegalName.Value == nil || *stub.profilePatch.LegalName.Value != " BridgeWorks Ltd " {
		t.Fatalf("legal name patch = %#v", stub.profilePatch.LegalName)
	}
	if !stub.profilePatch.Country.Set || stub.profilePatch.Country.Value == nil || *stub.profilePatch.Country.Value != "vn" {
		t.Fatalf("country patch = %#v", stub.profilePatch.Country)
	}
	if strings.Contains(recorder.Body.String(), "clerk") {
		t.Fatalf("response exposed provider detail: %s", recorder.Body.String())
	}
}

func TestPatchCurrentOrganizationRejectsUnknownFieldsBeforeMutation(t *testing.T) {
	organizationID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000211")
	actor := authorization.NewActorContext(uuid.New(), organizationID, uuid.New(), "admin", []string{authorization.PermissionOrganizationManage})
	resolver := onboardingResolverStub{result: currentorganization.Result{Actor: actor}}
	stub := &onboardingStub{}
	request := httptest.NewRequest(http.MethodPatch, "/organizations/current", strings.NewReader("{\"verification_status\":\"verified\"}"))
	request = request.WithContext(authn.ContextWithPrincipal(request.Context(), authorization.Principal{
		ClerkUserID: "verified-user", SessionID: "verified-session", ClerkOrganizationID: "verified-org",
	}))
	recorder := httptest.NewRecorder()

	PatchCurrentOrganization(resolver, stub).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "\"code\":\"invalid_request\"") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	if stub.profileActor.OrganizationID != uuid.Nil {
		t.Fatal("onboarding mutation was called for invalid input")
	}
}

func TestRequestVerificationMapsTransitionConflict(t *testing.T) {
	organizationID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000221")
	actor := authorization.NewActorContext(uuid.New(), organizationID, uuid.New(), "owner", []string{authorization.PermissionOrganizationVerifyRequest})
	resolver := onboardingResolverStub{result: currentorganization.Result{Actor: actor}}
	stub := &onboardingStub{verifyErr: organizationonboarding.ErrVerificationTransitionNotAllowed}
	request := httptest.NewRequest(http.MethodPost, "/organizations/current/verification", strings.NewReader("{}"))
	request = request.WithContext(authn.ContextWithPrincipal(request.Context(), authorization.Principal{
		ClerkUserID: "verified-user", SessionID: "verified-session", ClerkOrganizationID: "verified-org",
	}))
	recorder := httptest.NewRecorder()

	RequestCurrentOrganizationVerification(resolver, stub).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "\"code\":\"verification_transition_not_allowed\"") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	if recorder.Header().Get("Retry-After") != "" {
		t.Fatalf("Retry-After = %q for lifecycle conflict", recorder.Header().Get("Retry-After"))
	}
}

func TestPatchCurrentOrganizationPreservesNotReadyContract(t *testing.T) {
	resolver := onboardingResolverStub{err: currentorganization.ErrOrganizationNotReady}
	stub := &onboardingStub{}
	request := httptest.NewRequest(http.MethodPatch, "/organizations/current", strings.NewReader("{\"country\":\"VN\"}"))
	request = request.WithContext(authn.ContextWithPrincipal(request.Context(), authorization.Principal{
		ClerkUserID: "verified-user", SessionID: "verified-session", ClerkOrganizationID: "verified-org",
	}))
	recorder := httptest.NewRecorder()

	PatchCurrentOrganization(resolver, stub).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Retry-After") != "2" {
		t.Fatalf("Retry-After = %q", recorder.Header().Get("Retry-After"))
	}
	if !strings.Contains(recorder.Body.String(), "\"code\":\"organization_not_ready\"") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestRequestVerificationPreservesNotReadyRetryHeader(t *testing.T) {
	resolver := onboardingResolverStub{err: currentorganization.ErrOrganizationNotReady}
	stub := &onboardingStub{}
	request := httptest.NewRequest(http.MethodPost, "/organizations/current/verification", strings.NewReader("{}"))
	request = request.WithContext(authn.ContextWithPrincipal(request.Context(), authorization.Principal{
		ClerkUserID: "verified-user", SessionID: "verified-session", ClerkOrganizationID: "verified-org",
	}))
	recorder := httptest.NewRecorder()

	RequestCurrentOrganizationVerification(resolver, stub).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Retry-After") != "2" {
		t.Fatalf("Retry-After = %q", recorder.Header().Get("Retry-After"))
	}
	if !strings.Contains(recorder.Body.String(), "\"code\":\"organization_not_ready\"") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestOnboardingErrorDelegatesPermissionDenied(t *testing.T) {
	request := httptest.NewRequest(http.MethodPatch, "/organizations/current", nil)
	recorder := httptest.NewRecorder()
	writeOnboardingError(recorder, request, currentorganization.ErrPermissionDenied)
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "\"code\":\"permission_denied\"") {
		t.Fatalf("status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestOnboardingErrorDoesNotExposeRawErrors(t *testing.T) {
	request := httptest.NewRequest(http.MethodPatch, "/organizations/current", nil)
	recorder := httptest.NewRecorder()
	writeOnboardingError(recorder, request, errors.New("postgres://secret dependency failure"))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "postgres://") || strings.Contains(recorder.Body.String(), "secret") {
		t.Fatalf("raw error leaked: %s", recorder.Body.String())
	}
}
