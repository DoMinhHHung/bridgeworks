package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authn"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
	"github.com/google/uuid"
)

func TestCurrentOrganizationIncludesBridgeWorksProductFields(t *testing.T) {
	organizationID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000011")
	membershipID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000012")
	identityID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000013")
	name := "BridgeWorks"
	slug := "bridgeworks"
	legalName := "BridgeWorks Company Limited"
	website := "https://bridgeworks.example"
	country := "VN"
	companyType := "agency"

	resolver := &fakeCurrentResolver{result: currentorganization.Result{
		Actor: authorization.NewActorContext(
			identityID,
			organizationID,
			membershipID,
			"viewer",
			[]string{authorization.PermissionOrganizationRead},
		),
		Organization: currentorganization.Organization{
			ID:                 organizationID,
			Name:               &name,
			Slug:               &slug,
			Status:             "active",
			LegalName:          &legalName,
			Website:            &website,
			Country:            &country,
			CompanyType:        &companyType,
			VerificationStatus: "pending",
			TrustStatus:        "unassessed",
		},
		Membership: currentorganization.Membership{
			ID:              membershipID,
			OrganizationID:  organizationID,
			ApplicationRole: "viewer",
			Status:          "active",
		},
	}}

	handler := RequestID(AuthenticatedResponseHeaders(CurrentOrganization(resolver)))
	request := httptest.NewRequest(http.MethodGet, "/organizations/current", nil)
	request.Header.Set("Authorization", "Bearer redacted")
	request = request.WithContext(authn.ContextWithPrincipal(request.Context(), authorization.Principal{
		ClerkUserID:         "user-test",
		ClerkOrganizationID: "org-test",
	}))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	var payload organizationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.LegalName == nil || *payload.LegalName != legalName {
		t.Fatalf("legal_name = %#v", payload.LegalName)
	}
	if payload.Website == nil || *payload.Website != website {
		t.Fatalf("website = %#v", payload.Website)
	}
	if payload.Country == nil || *payload.Country != country {
		t.Fatalf("country = %#v", payload.Country)
	}
	if payload.CompanyType == nil || *payload.CompanyType != companyType {
		t.Fatalf("company_type = %#v", payload.CompanyType)
	}
	if payload.VerificationStatus != "pending" || payload.TrustStatus != "unassessed" {
		t.Fatalf("verification/trust = %q/%q", payload.VerificationStatus, payload.TrustStatus)
	}
}
