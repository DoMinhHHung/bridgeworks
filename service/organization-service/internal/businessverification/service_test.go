package businessverification

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/google/uuid"
)

var (
	businessTestIdentityID     = uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000071")
	businessTestOrganizationID = uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000072")
	businessTestMembershipID   = uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000073")
)

type fakeBusinessFactory struct{ uow *fakeBusinessUOW }

func (f fakeBusinessFactory) BeginBusinessVerification(context.Context) (UnitOfWork, error) {
	return f.uow, nil
}

type fakeBusinessUOW struct {
	membership Membership
	found      bool
	persisted  bool
	domain     string
	verifiedAt time.Time
	verifiedBy uuid.UUID
	commits    int
	rollbacks  int
}

func (u *fakeBusinessUOW) AcquireOrganizationLock(context.Context, uuid.UUID) error { return nil }
func (u *fakeBusinessUOW) LockMembership(context.Context, uuid.UUID, uuid.UUID) (Membership, bool, error) {
	return u.membership, u.found, nil
}
func (u *fakeBusinessUOW) UpdateBusinessEmailVerification(_ context.Context, _ uuid.UUID, domain string, verifiedAt time.Time, verifiedBy uuid.UUID) error {
	u.persisted = true
	u.domain = domain
	u.verifiedAt = verifiedAt
	u.verifiedBy = verifiedBy
	return nil
}
func (u *fakeBusinessUOW) Commit(context.Context) error   { u.commits++; return nil }
func (u *fakeBusinessUOW) Rollback(context.Context) error { u.rollbacks++; return nil }

func businessActor() authorization.ActorContext {
	return authorization.NewActorContext(
		businessTestIdentityID,
		businessTestOrganizationID,
		businessTestMembershipID,
		"owner",
		nil,
	)
}

func TestVerifyBusinessEmailUsesVerifiedIdentityDomainOnly(t *testing.T) {
	uow := &fakeBusinessUOW{found: true, membership: Membership{
		ID: businessTestMembershipID, OrganizationID: businessTestOrganizationID,
		ApplicationRole: "owner", Status: "active",
	}}
	service, err := New(fakeBusinessFactory{uow: uow}, []string{"gmail.com", "outlook.com"})
	if err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixed }
	email := "  Person+tag@Example.Company  "

	result, err := service.Verify(context.Background(), businessActor(), &email)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if result.Domain != "example.company" || !result.VerifiedAt.Equal(fixed) {
		t.Fatalf("result = %#v", result)
	}
	if !uow.persisted || uow.domain != "example.company" || uow.verifiedBy != businessTestIdentityID {
		t.Fatalf("persisted proof = %v %q %s", uow.persisted, uow.domain, uow.verifiedBy)
	}
	if uow.commits != 1 || uow.rollbacks != 0 {
		t.Fatalf("commit/rollback = %d/%d", uow.commits, uow.rollbacks)
	}
}

func TestVerifyBusinessEmailRequiresVerifiedPrimaryEmail(t *testing.T) {
	service, err := New(fakeBusinessFactory{uow: &fakeBusinessUOW{}}, []string{"gmail.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Verify(context.Background(), businessActor(), nil); !errors.Is(err, ErrVerifiedPrimaryEmailRequired) {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestVerifyBusinessEmailRejectsPersonalMalformedAndUnicodeDomains(t *testing.T) {
	service, err := New(fakeBusinessFactory{uow: &fakeBusinessUOW{}}, []string{"gmail.com"})
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name  string
		email string
		want  error
	}{
		{name: "personal", email: "person@Mail.GMAIL.com", want: ErrPersonalEmailDomain},
		{name: "malformed", email: "person@example", want: ErrInvalidBusinessEmail},
		{name: "unicode", email: "person@café.example", want: ErrInvalidBusinessEmail},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := service.Verify(context.Background(), businessActor(), &testCase.email); !errors.Is(err, testCase.want) {
				t.Fatalf("Verify() error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestVerifyBusinessEmailRechecksCurrentLocalRole(t *testing.T) {
	for _, role := range []string{"viewer", "recruiter", "delivery_manager"} {
		t.Run(role, func(t *testing.T) {
			uow := &fakeBusinessUOW{found: true, membership: Membership{
				ID: businessTestMembershipID, OrganizationID: businessTestOrganizationID,
				ApplicationRole: role, Status: "active",
			}}
			service, err := New(fakeBusinessFactory{uow: uow}, []string{"gmail.com"})
			if err != nil {
				t.Fatal(err)
			}
			email := "person@company.example"
			if _, err := service.Verify(context.Background(), businessActor(), &email); !errors.Is(err, ErrPermissionDenied) {
				t.Fatalf("Verify() error = %v", err)
			}
			if uow.persisted || uow.commits != 0 || uow.rollbacks != 1 {
				t.Fatalf("unauthorized mutation = persisted:%v commit:%d rollback:%d", uow.persisted, uow.commits, uow.rollbacks)
			}
		})
	}
}

func TestVerifyBusinessEmailAllowsCurrentAdmin(t *testing.T) {
	uow := &fakeBusinessUOW{found: true, membership: Membership{
		ID: businessTestMembershipID, OrganizationID: businessTestOrganizationID,
		ApplicationRole: "admin", Status: "active",
	}}
	service, err := New(fakeBusinessFactory{uow: uow}, []string{"gmail.com"})
	if err != nil {
		t.Fatal(err)
	}
	email := "admin@company.example"
	if _, err := service.Verify(context.Background(), businessActor(), &email); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}
