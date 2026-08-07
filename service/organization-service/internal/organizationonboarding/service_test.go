package organizationonboarding

import (
	"context"
	"errors"
	"testing"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
	"github.com/google/uuid"
)

type fakeFactory struct{ uow *fakeUnitOfWork }

func (f fakeFactory) BeginOnboarding(context.Context) (UnitOfWork, error) { return f.uow, nil }

type fakeUnitOfWork struct {
	organization  currentorganization.Organization
	found         bool
	updated       bool
	verifyUpdate  bool
	commitCalls   int
	rollbackCalls int
}

func (u *fakeUnitOfWork) LockOrganization(context.Context, uuid.UUID) (currentorganization.Organization, bool, error) {
	return u.organization, u.found, nil
}
func (u *fakeUnitOfWork) UpdateProductProfile(_ context.Context, _ uuid.UUID, legalName, website, country, companyType *string) (currentorganization.Organization, error) {
	u.updated = true
	u.organization.LegalName = legalName
	u.organization.Website = website
	u.organization.Country = country
	u.organization.CompanyType = companyType
	return u.organization, nil
}
func (u *fakeUnitOfWork) UpdateVerificationStatus(_ context.Context, _ uuid.UUID, status string) (currentorganization.Organization, error) {
	u.verifyUpdate = true
	u.organization.VerificationStatus = status
	return u.organization, nil
}
func (u *fakeUnitOfWork) Commit(context.Context) error   { u.commitCalls++; return nil }
func (u *fakeUnitOfWork) Rollback(context.Context) error { u.rollbackCalls++; return nil }

func TestUpdateProfileNormalizesAndPreservesOmittedFields(t *testing.T) {
	organizationID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000101")
	oldWebsite := "https://old.example/path"
	legalName := "  BridgeWorks Company Limited  "
	country := "vn"
	companyType := "Software-Agency"
	uow := &fakeUnitOfWork{
		found: true,
		organization: currentorganization.Organization{
			ID: organizationID, Status: "active", Website: &oldWebsite,
			VerificationStatus: "unverified", TrustStatus: "unassessed",
		},
	}
	service := New(fakeFactory{uow: uow})
	actor := authorization.NewActorContext(uuid.New(), organizationID, uuid.New(), "admin", []string{authorization.PermissionOrganizationManage})

	got, err := service.UpdateProfile(context.Background(), actor, ProfilePatch{
		LegalName:   StringPatch{Set: true, Value: &legalName},
		Country:     StringPatch{Set: true, Value: &country},
		CompanyType: StringPatch{Set: true, Value: &companyType},
	})
	if err != nil {
		t.Fatalf("UpdateProfile() error = %v", err)
	}
	if got.LegalName == nil || *got.LegalName != "BridgeWorks Company Limited" {
		t.Fatalf("legal_name = %#v", got.LegalName)
	}
	if got.Website == nil || *got.Website != oldWebsite {
		t.Fatalf("website was not preserved: %#v", got.Website)
	}
	if got.Country == nil || *got.Country != "VN" {
		t.Fatalf("country = %#v", got.Country)
	}
	if got.CompanyType == nil || *got.CompanyType != "software-agency" {
		t.Fatalf("company_type = %#v", got.CompanyType)
	}
	if !uow.updated || uow.commitCalls != 1 || uow.rollbackCalls != 0 {
		t.Fatalf("update/commit/rollback = %v/%d/%d", uow.updated, uow.commitCalls, uow.rollbackCalls)
	}
}

func TestUpdateProfileRequiresLocalPermission(t *testing.T) {
	service := New(fakeFactory{uow: &fakeUnitOfWork{}})
	actor := authorization.NewActorContext(uuid.New(), uuid.New(), uuid.New(), "viewer", []string{authorization.PermissionOrganizationRead})
	value := "VN"
	_, err := service.UpdateProfile(context.Background(), actor, ProfilePatch{Country: StringPatch{Set: true, Value: &value}})
	if !errors.Is(err, currentorganization.ErrPermissionDenied) {
		t.Fatalf("UpdateProfile() error = %v", err)
	}
}

func TestRequestVerificationTransitionsAndIsIdempotent(t *testing.T) {
	organizationID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000102")
	actor := authorization.NewActorContext(uuid.New(), organizationID, uuid.New(), "owner", []string{authorization.PermissionOrganizationVerifyRequest})

	for _, initial := range []string{"unverified", "rejected", "pending"} {
		t.Run(initial, func(t *testing.T) {
			uow := &fakeUnitOfWork{found: true, organization: currentorganization.Organization{
				ID: organizationID, Status: "active", VerificationStatus: initial, TrustStatus: "unassessed",
			}}
			got, err := New(fakeFactory{uow: uow}).RequestVerification(context.Background(), actor)
			if err != nil {
				t.Fatalf("RequestVerification() error = %v", err)
			}
			if got.VerificationStatus != "pending" {
				t.Fatalf("verification_status = %q", got.VerificationStatus)
			}
			if initial == "pending" && uow.verifyUpdate {
				t.Fatal("idempotent pending request performed an update")
			}
			if initial != "pending" && !uow.verifyUpdate {
				t.Fatal("transition did not update verification status")
			}
		})
	}
}

func TestRequestVerificationRejectsVerifiedOrganization(t *testing.T) {
	organizationID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000103")
	uow := &fakeUnitOfWork{found: true, organization: currentorganization.Organization{
		ID: organizationID, Status: "active", VerificationStatus: "verified", TrustStatus: "unassessed",
	}}
	actor := authorization.NewActorContext(uuid.New(), organizationID, uuid.New(), "admin", []string{authorization.PermissionOrganizationVerifyRequest})
	_, err := New(fakeFactory{uow: uow}).RequestVerification(context.Background(), actor)
	if !errors.Is(err, ErrVerificationTransitionNotAllowed) {
		t.Fatalf("RequestVerification() error = %v", err)
	}
	if uow.commitCalls != 0 || uow.rollbackCalls != 1 {
		t.Fatalf("commit/rollback = %d/%d", uow.commitCalls, uow.rollbackCalls)
	}
}

func TestNormalizeWebsiteRejectsCredentialsAndFragments(t *testing.T) {
	for _, value := range []string{
		"https://user:password@example.com",
		"https://example.com/path#private",
		"ftp://example.com",
		"/relative/path",
	} {
		patch := ProfilePatch{Website: StringPatch{Set: true, Value: &value}}
		if _, err := NormalizeProfilePatch(patch); !errors.Is(err, ErrInvalidProfile) {
			t.Fatalf("NormalizeProfilePatch(%q) error = %v", value, err)
		}
	}
}

func TestNormalizeCountryRequiresISOAlpha2(t *testing.T) {
	for _, value := range []string{"Vietnam", "ZZ", "V", "123"} {
		patch := ProfilePatch{Country: StringPatch{Set: true, Value: &value}}
		if _, err := NormalizeProfilePatch(patch); !errors.Is(err, ErrInvalidProfile) {
			t.Fatalf("NormalizeProfilePatch(%q) error = %v", value, err)
		}
	}
}