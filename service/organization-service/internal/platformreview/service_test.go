package platformreview

import (
	"context"
	"errors"
	"testing"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationaudit"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platformauthorization"
	"github.com/google/uuid"
)

var (
	reviewTestIdentityID     = uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000a1")
	reviewTestOrganizationID = uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000a2")
)

type fakeIdentityReader struct {
	identity currentorganization.Identity
	err      error
	calls    int
}

func (f *fakeIdentityReader) Resolve(context.Context, string, string) (currentorganization.Identity, error) {
	f.calls++
	return f.identity, f.err
}

type fakeAccessReader struct {
	access platformauthorization.Access
	err    error
	calls  int
}

func (f *fakeAccessReader) Resolve(context.Context, string, string) (platformauthorization.Access, error) {
	f.calls++
	return f.access, f.err
}

type fakeReviewFactory struct{ uow *fakeReviewUOW }

func (f fakeReviewFactory) BeginPlatformReview(context.Context) (ReviewUnitOfWork, error) {
	return f.uow, nil
}

type fakeReviewUOW struct {
	organization currentorganization.Organization
	found        bool
	audits       []organizationaudit.Event
	updates      int
	commits      int
	rollbacks    int
	auditErr     error
}

func (u *fakeReviewUOW) LockOrganization(context.Context, uuid.UUID) (currentorganization.Organization, bool, error) {
	return u.organization, u.found, nil
}
func (u *fakeReviewUOW) UpdateVerificationStatus(_ context.Context, _ uuid.UUID, status string) (currentorganization.Organization, error) {
	u.updates++
	u.organization.VerificationStatus = status
	return u.organization, nil
}
func (u *fakeReviewUOW) InsertAuditEvent(_ context.Context, event organizationaudit.Event) error {
	if u.auditErr != nil {
		return u.auditErr
	}
	u.audits = append(u.audits, event)
	return nil
}
func (u *fakeReviewUOW) Commit(context.Context) error   { u.commits++; return nil }
func (u *fakeReviewUOW) Rollback(context.Context) error { u.rollbacks++; return nil }

func TestAuthorizeReviewerRequiresExplicitPlatformPermissionWithoutOrganizationContext(t *testing.T) {
	identity := &fakeIdentityReader{identity: currentorganization.Identity{ID: reviewTestIdentityID}}
	access := &fakeAccessReader{access: platformauthorization.Access{
		Roles:       []string{platformauthorization.RolePlatformAdmin},
		Permissions: []string{platformauthorization.PermissionOrganizationVerificationReview},
	}}
	service := New(identity, access, nil, nil)

	reviewer, err := service.AuthorizeReviewer(context.Background(), "Bearer session", "request-id")
	if err != nil {
		t.Fatalf("AuthorizeReviewer() error = %v", err)
	}
	if reviewer.IdentityUserID != reviewTestIdentityID || identity.calls != 1 || access.calls != 1 {
		t.Fatalf("reviewer=%#v identity_calls=%d access_calls=%d", reviewer, identity.calls, access.calls)
	}
}

func TestAuthorizeReviewerRejectsOrdinaryUser(t *testing.T) {
	identity := &fakeIdentityReader{identity: currentorganization.Identity{ID: reviewTestIdentityID}}
	access := &fakeAccessReader{access: platformauthorization.Access{}}
	service := New(identity, access, nil, nil)
	if _, err := service.AuthorizeReviewer(context.Background(), "Bearer session", "request-id"); !errors.Is(err, platformauthorization.ErrPermissionDenied) {
		t.Fatalf("AuthorizeReviewer() error = %v", err)
	}
}

func TestDecisionTransitionsAndAuditsOnce(t *testing.T) {
	uow := &fakeReviewUOW{found: true, organization: currentorganization.Organization{
		ID: reviewTestOrganizationID, Status: "active", VerificationStatus: "pending", TrustStatus: "unassessed",
	}}
	service := New(nil, nil, nil, fakeReviewFactory{uow: uow})
	reviewer := Reviewer{IdentityUserID: reviewTestIdentityID}

	got, err := service.Decide(context.Background(), reviewer, reviewTestOrganizationID.String(), DecisionVerified)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if got.VerificationStatus != DecisionVerified || got.TrustStatus != "unassessed" {
		t.Fatalf("organization = %#v", got)
	}
	if uow.updates != 1 || uow.commits != 1 || uow.rollbacks != 0 || len(uow.audits) != 1 {
		t.Fatalf("updates/commits/rollbacks/audits = %d/%d/%d/%d", uow.updates, uow.commits, uow.rollbacks, len(uow.audits))
	}
	event := uow.audits[0]
	if event.EventType != organizationaudit.EventOrganizationVerificationReviewed || event.ActorKind != organizationaudit.ActorPlatformAdmin || event.ActorIdentityUserID == nil || *event.ActorIdentityUserID != reviewTestIdentityID {
		t.Fatalf("audit event = %#v", event)
	}
	if event.FromValue == nil || *event.FromValue != "pending" || event.ToValue == nil || *event.ToValue != "verified" {
		t.Fatalf("audit transition = %#v", event)
	}
}

func TestSameTerminalDecisionIsIdempotentWithoutAudit(t *testing.T) {
	uow := &fakeReviewUOW{found: true, organization: currentorganization.Organization{
		ID: reviewTestOrganizationID, Status: "active", VerificationStatus: "verified",
	}}
	service := New(nil, nil, nil, fakeReviewFactory{uow: uow})
	if _, err := service.Decide(context.Background(), Reviewer{IdentityUserID: reviewTestIdentityID}, reviewTestOrganizationID.String(), DecisionVerified); err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if uow.updates != 0 || len(uow.audits) != 0 || uow.commits != 1 {
		t.Fatalf("idempotent mutation = updates:%d audits:%d commits:%d", uow.updates, len(uow.audits), uow.commits)
	}
}

func TestOppositeTerminalDecisionConflictsWithoutAudit(t *testing.T) {
	uow := &fakeReviewUOW{found: true, organization: currentorganization.Organization{
		ID: reviewTestOrganizationID, Status: "active", VerificationStatus: "verified",
	}}
	service := New(nil, nil, nil, fakeReviewFactory{uow: uow})
	_, err := service.Decide(context.Background(), Reviewer{IdentityUserID: reviewTestIdentityID}, reviewTestOrganizationID.String(), DecisionRejected)
	if !errors.Is(err, ErrDecisionConflict) {
		t.Fatalf("Decide() error = %v", err)
	}
	if uow.updates != 0 || len(uow.audits) != 0 || uow.commits != 0 || uow.rollbacks != 1 {
		t.Fatalf("conflict mutation = updates:%d audits:%d commits:%d rollbacks:%d", uow.updates, len(uow.audits), uow.commits, uow.rollbacks)
	}
}

func TestUnverifiedAndInactiveOrganizationsCannotBeReviewed(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		status       string
		verification string
		want         error
	}{
		{name: "unverified", status: "active", verification: "unverified", want: ErrVerificationNotPending},
		{name: "disabled", status: "disabled", verification: "pending", want: ErrOrganizationNotReviewable},
		{name: "deleted", status: "deleted", verification: "pending", want: ErrOrganizationNotReviewable},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			uow := &fakeReviewUOW{found: true, organization: currentorganization.Organization{
				ID: reviewTestOrganizationID, Status: testCase.status, VerificationStatus: testCase.verification,
			}}
			service := New(nil, nil, nil, fakeReviewFactory{uow: uow})
			_, err := service.Decide(context.Background(), Reviewer{IdentityUserID: reviewTestIdentityID}, reviewTestOrganizationID.String(), DecisionVerified)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("Decide() error = %v, want %v", err, testCase.want)
			}
			if uow.updates != 0 || len(uow.audits) != 0 {
				t.Fatalf("unexpected mutation updates=%d audits=%d", uow.updates, len(uow.audits))
			}
		})
	}
}

func TestAuditFailureRollsBackDecision(t *testing.T) {
	uow := &fakeReviewUOW{found: true, auditErr: errors.New("audit insert failed"), organization: currentorganization.Organization{
		ID: reviewTestOrganizationID, Status: "active", VerificationStatus: "pending",
	}}
	service := New(nil, nil, nil, fakeReviewFactory{uow: uow})
	if _, err := service.Decide(context.Background(), Reviewer{IdentityUserID: reviewTestIdentityID}, reviewTestOrganizationID.String(), DecisionVerified); err == nil {
		t.Fatal("Decide() error = nil")
	}
	if uow.commits != 0 || uow.rollbacks != 1 {
		t.Fatalf("commit/rollback = %d/%d", uow.commits, uow.rollbacks)
	}
}
