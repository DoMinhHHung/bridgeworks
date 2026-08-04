package organizationsync

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	testOrganizationID = uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000001")
	testMembershipID   = uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000002")
	testSecondID       = uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000003")
)

type fakeFactory struct {
	uow *fakeUnitOfWork
	err error
}

func (f fakeFactory) Begin(context.Context) (UnitOfWork, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.uow, nil
}

type fakeGenerator struct {
	ids   []uuid.UUID
	err   error
	calls int
}

func (g *fakeGenerator) New() (uuid.UUID, error) {
	g.calls++
	if g.err != nil {
		return uuid.Nil, g.err
	}
	if len(g.ids) == 0 {
		return uuid.Nil, errors.New("no fake UUID configured")
	}
	id := g.ids[0]
	g.ids = g.ids[1:]
	return id, nil
}

type organizationUpdate struct {
	clerkID string
	name    *string
	slug    *string
	status  string
}

type membershipRoleUpdate struct {
	clerkID string
	role    *string
}

type fakeUnitOfWork struct {
	insertedInbox  bool
	insertInboxErr error
	lockErr        error
	latest         Event
	latestFound    bool
	latestErr      error

	organization               Organization
	organizationFound          bool
	getOrganizationErr         error
	insertOrganizationErr      error
	updateOrganizationErr      error
	markOrganizationDeletedErr error

	membership               Membership
	membershipFound          bool
	getMembershipErr         error
	activeMembership         Membership
	activeFound              bool
	getActiveErr             error
	insertMembershipErr      error
	updateMembershipErr      error
	markMembershipDeletedErr error
	savepointErr             error
	rollbackSavepointErr     error
	releaseSavepointErr      error

	commitErr   error
	rollbackErr error

	sequence                     []string
	lockCalls                    int
	latestCalls                  int
	getOrganizationCalls         int
	insertedOrganizations        []Organization
	organizationUpdates          []organizationUpdate
	markOrganizationDeletedCalls int
	getMembershipCalls           int
	getActiveCalls               int
	insertedMemberships          []Membership
	membershipRoleUpdates        []membershipRoleUpdate
	markMembershipDeletedCalls   int
	commitCalls                  int
	rollbackCalls                int
}

func newFakeUnitOfWork() *fakeUnitOfWork {
	return &fakeUnitOfWork{insertedInbox: true}
}

func (u *fakeUnitOfWork) InsertInbox(context.Context, Event) (bool, error) {
	u.sequence = append(u.sequence, "insert_inbox")
	return u.insertedInbox, u.insertInboxErr
}
func (u *fakeUnitOfWork) AcquireOrganizationLock(context.Context, string) error {
	u.sequence = append(u.sequence, "lock")
	u.lockCalls++
	return u.lockErr
}
func (u *fakeUnitOfWork) LatestAggregateEvent(context.Context, string, string, string) (Event, bool, error) {
	u.sequence = append(u.sequence, "latest")
	u.latestCalls++
	return u.latest, u.latestFound, u.latestErr
}
func (u *fakeUnitOfWork) GetOrganization(context.Context, string) (Organization, bool, error) {
	u.sequence = append(u.sequence, "get_organization")
	u.getOrganizationCalls++
	return u.organization, u.organizationFound, u.getOrganizationErr
}
func (u *fakeUnitOfWork) InsertOrganization(_ context.Context, organization Organization) error {
	u.sequence = append(u.sequence, "insert_organization")
	u.insertedOrganizations = append(u.insertedOrganizations, organization)
	return u.insertOrganizationErr
}
func (u *fakeUnitOfWork) UpdateOrganizationProjection(_ context.Context, clerkID string, name, slug *string, status string) error {
	u.sequence = append(u.sequence, "update_organization")
	u.organizationUpdates = append(u.organizationUpdates, organizationUpdate{clerkID: clerkID, name: name, slug: slug, status: status})
	return u.updateOrganizationErr
}
func (u *fakeUnitOfWork) MarkOrganizationDeleted(context.Context, string) error {
	u.sequence = append(u.sequence, "delete_organization")
	u.markOrganizationDeletedCalls++
	return u.markOrganizationDeletedErr
}
func (u *fakeUnitOfWork) GetMembership(context.Context, string) (Membership, bool, error) {
	u.sequence = append(u.sequence, "get_membership")
	u.getMembershipCalls++
	return u.membership, u.membershipFound, u.getMembershipErr
}
func (u *fakeUnitOfWork) GetActiveMembership(context.Context, uuid.UUID, string) (Membership, bool, error) {
	u.sequence = append(u.sequence, "get_active_membership")
	u.getActiveCalls++
	return u.activeMembership, u.activeFound, u.getActiveErr
}
func (u *fakeUnitOfWork) CreateMembershipInsertSavepoint(context.Context) error {
	u.sequence = append(u.sequence, "savepoint")
	return u.savepointErr
}
func (u *fakeUnitOfWork) RollbackMembershipInsertSavepoint(context.Context) error {
	u.sequence = append(u.sequence, "rollback_savepoint")
	return u.rollbackSavepointErr
}
func (u *fakeUnitOfWork) ReleaseMembershipInsertSavepoint(context.Context) error {
	u.sequence = append(u.sequence, "release_savepoint")
	return u.releaseSavepointErr
}
func (u *fakeUnitOfWork) InsertMembership(_ context.Context, membership Membership) error {
	u.sequence = append(u.sequence, "insert_membership")
	u.insertedMemberships = append(u.insertedMemberships, membership)
	return u.insertMembershipErr
}
func (u *fakeUnitOfWork) UpdateMembershipClerkRole(_ context.Context, clerkID string, role *string) error {
	u.sequence = append(u.sequence, "update_membership_role")
	u.membershipRoleUpdates = append(u.membershipRoleUpdates, membershipRoleUpdate{clerkID: clerkID, role: role})
	return u.updateMembershipErr
}
func (u *fakeUnitOfWork) MarkMembershipDeleted(context.Context, string) error {
	u.sequence = append(u.sequence, "delete_membership")
	u.markMembershipDeletedCalls++
	return u.markMembershipDeletedErr
}
func (u *fakeUnitOfWork) Commit(context.Context) error {
	u.sequence = append(u.sequence, "commit")
	u.commitCalls++
	return u.commitErr
}
func (u *fakeUnitOfWork) Rollback(context.Context) error {
	u.sequence = append(u.sequence, "rollback")
	u.rollbackCalls++
	return u.rollbackErr
}

func TestServiceDuplicateCommitsBeforeAdvisoryLock(t *testing.T) {
	uow := newFakeUnitOfWork()
	uow.insertedInbox = false
	service := New(fakeFactory{uow: uow}, &fakeGenerator{})

	if err := service.Process(context.Background(), organizationEvent(EventOrganizationCreated)); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if uow.lockCalls != 0 || uow.latestCalls != 0 || uow.getOrganizationCalls != 0 {
		t.Fatalf("duplicate performed aggregate work: %#v", uow.sequence)
	}
	if uow.commitCalls != 1 || uow.rollbackCalls != 0 {
		t.Fatalf("commit/rollback = %d/%d", uow.commitCalls, uow.rollbackCalls)
	}
}

func TestServiceStaleEventCommitsInboxWithoutMutation(t *testing.T) {
	uow := newFakeUnitOfWork()
	uow.latestFound = true
	uow.latest = Event{EventID: "evt-new", Type: EventOrganizationUpdated, OccurredAt: time.Unix(20, 0)}
	service := New(fakeFactory{uow: uow}, &fakeGenerator{})
	event := organizationEvent(EventOrganizationCreated)
	event.OccurredAt = time.Unix(10, 0)

	if err := service.Process(context.Background(), event); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if uow.getOrganizationCalls != 0 || len(uow.insertedOrganizations) != 0 {
		t.Fatalf("stale event mutated aggregate: %#v", uow.sequence)
	}
	if uow.commitCalls != 1 || uow.rollbackCalls != 0 {
		t.Fatalf("commit/rollback = %d/%d", uow.commitCalls, uow.rollbackCalls)
	}
}

func TestServiceOrganizationTransitions(t *testing.T) {
	name := "BridgeWorks"
	slug := "bridgeworks"
	tests := []struct {
		name         string
		eventType    string
		existing     Organization
		found        bool
		wantInsert   string
		wantUpdate   string
		wantDelete   int
		generatorIDs []uuid.UUID
	}{
		{name: "create", eventType: EventOrganizationCreated, wantInsert: "active", generatorIDs: []uuid.UUID{testOrganizationID}},
		{name: "pending promotion", eventType: EventOrganizationCreated, existing: Organization{Status: "pending"}, found: true, wantUpdate: "active"},
		{name: "disabled update", eventType: EventOrganizationUpdated, existing: Organization{Status: "disabled"}, found: true, wantUpdate: "disabled"},
		{name: "deleted never restores", eventType: EventOrganizationCreated, existing: Organization{Status: "deleted"}, found: true},
		{name: "delete tombstone", eventType: EventOrganizationDeleted, wantInsert: "deleted", generatorIDs: []uuid.UUID{testOrganizationID}},
		{name: "delete existing", eventType: EventOrganizationDeleted, existing: Organization{Status: "active"}, found: true, wantDelete: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uow := newFakeUnitOfWork()
			uow.organization = tt.existing
			uow.organizationFound = tt.found
			generator := &fakeGenerator{ids: tt.generatorIDs}
			service := New(fakeFactory{uow: uow}, generator)
			event := organizationEvent(tt.eventType)
			event.Organization = OrganizationProjection{Name: &name, Slug: &slug}

			if err := service.Process(context.Background(), event); err != nil {
				t.Fatalf("Process() error = %v", err)
			}
			if tt.wantInsert == "" && len(uow.insertedOrganizations) != 0 {
				t.Fatalf("unexpected insert: %#v", uow.insertedOrganizations)
			}
			if tt.wantInsert != "" {
				if len(uow.insertedOrganizations) != 1 || uow.insertedOrganizations[0].Status != tt.wantInsert {
					t.Fatalf("inserted organization = %#v", uow.insertedOrganizations)
				}
			}
			if tt.wantUpdate == "" && len(uow.organizationUpdates) != 0 {
				t.Fatalf("unexpected update: %#v", uow.organizationUpdates)
			}
			if tt.wantUpdate != "" {
				if len(uow.organizationUpdates) != 1 || uow.organizationUpdates[0].status != tt.wantUpdate {
					t.Fatalf("organization updates = %#v", uow.organizationUpdates)
				}
			}
			if uow.markOrganizationDeletedCalls != tt.wantDelete {
				t.Fatalf("delete calls = %d, want %d", uow.markOrganizationDeletedCalls, tt.wantDelete)
			}
		})
	}
}

func TestServiceMembershipFirstCreatesPendingOrganizationAndRole(t *testing.T) {
	admin := ClerkRoleAdmin
	uow := newFakeUnitOfWork()
	generator := &fakeGenerator{ids: []uuid.UUID{testOrganizationID, testMembershipID}}
	service := New(fakeFactory{uow: uow}, generator)

	if err := service.Process(context.Background(), membershipEvent(EventMembershipCreated, &admin)); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if len(uow.insertedOrganizations) != 1 || uow.insertedOrganizations[0].Status != "pending" {
		t.Fatalf("pending organization = %#v", uow.insertedOrganizations)
	}
	if len(uow.insertedMemberships) != 1 || uow.insertedMemberships[0].ApplicationRole != RoleAdmin {
		t.Fatalf("membership = %#v", uow.insertedMemberships)
	}
	if !reflect.DeepEqual(uow.sequence[len(uow.sequence)-3:], []string{"insert_membership", "release_savepoint", "commit"}) {
		t.Fatalf("savepoint success sequence = %#v", uow.sequence)
	}
}

func TestServiceUnknownClerkRoleInitializesViewer(t *testing.T) {
	unknown := "org:billing"
	uow := newFakeUnitOfWork()
	uow.organizationFound = true
	uow.organization = Organization{ID: testOrganizationID, Status: "active"}
	service := New(fakeFactory{uow: uow}, &fakeGenerator{ids: []uuid.UUID{testMembershipID}})

	if err := service.Process(context.Background(), membershipEvent(EventMembershipCreated, &unknown)); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if got := uow.insertedMemberships[0].ApplicationRole; got != RoleViewer {
		t.Fatalf("application role = %q", got)
	}
}

func TestServiceMembershipUpdatePreservesApplicationRole(t *testing.T) {
	memberRole := "org:member"
	uow := newFakeUnitOfWork()
	uow.organizationFound = true
	uow.organization = Organization{ID: testOrganizationID, Status: "active"}
	uow.membershipFound = true
	uow.membership = Membership{
		ID: testMembershipID, ClerkMembershipID: "mem-1", OrganizationID: testOrganizationID,
		ClerkUserID: "user-1", ApplicationRole: RoleAdmin, Status: "active",
	}
	service := New(fakeFactory{uow: uow}, &fakeGenerator{})

	if err := service.Process(context.Background(), membershipEvent(EventMembershipUpdated, &memberRole)); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if len(uow.membershipRoleUpdates) != 1 || uow.membershipRoleUpdates[0].role == nil || *uow.membershipRoleUpdates[0].role != memberRole {
		t.Fatalf("role updates = %#v", uow.membershipRoleUpdates)
	}
	if len(uow.insertedMemberships) != 0 {
		t.Fatalf("membership update inserted a row")
	}
}

func TestServiceDeletedMembershipNeverRestores(t *testing.T) {
	uow := newFakeUnitOfWork()
	uow.organizationFound = true
	uow.organization = Organization{ID: testOrganizationID, Status: "active"}
	uow.membershipFound = true
	uow.membership = Membership{ID: testMembershipID, Status: "deleted"}
	service := New(fakeFactory{uow: uow}, &fakeGenerator{})

	if err := service.Process(context.Background(), membershipEvent(EventMembershipCreated, nil)); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if len(uow.membershipRoleUpdates) != 0 || len(uow.insertedMemberships) != 0 {
		t.Fatalf("deleted membership restored: %#v", uow.sequence)
	}
}

func TestServiceRollsBackOnGeneratorAndPersistenceErrors(t *testing.T) {
	t.Run("generator", func(t *testing.T) {
		uow := newFakeUnitOfWork()
		service := New(fakeFactory{uow: uow}, &fakeGenerator{err: errors.New("entropy unavailable")})
		if err := service.Process(context.Background(), organizationEvent(EventOrganizationCreated)); err == nil {
			t.Fatal("Process() error = nil")
		}
		if uow.rollbackCalls != 1 || uow.commitCalls != 0 {
			t.Fatalf("commit/rollback = %d/%d", uow.commitCalls, uow.rollbackCalls)
		}
	})

	t.Run("persistence", func(t *testing.T) {
		uow := newFakeUnitOfWork()
		uow.organizationFound = true
		uow.organization = Organization{Status: "active"}
		uow.updateOrganizationErr = errors.New("database unavailable")
		service := New(fakeFactory{uow: uow}, &fakeGenerator{})
		if err := service.Process(context.Background(), organizationEvent(EventOrganizationUpdated)); err == nil {
			t.Fatal("Process() error = nil")
		}
		if uow.rollbackCalls != 1 || uow.commitCalls != 0 {
			t.Fatalf("commit/rollback = %d/%d", uow.commitCalls, uow.rollbackCalls)
		}
	})
}

func TestServiceCommitErrorIsReturnedAndRolledBack(t *testing.T) {
	uow := newFakeUnitOfWork()
	uow.insertedInbox = false
	uow.commitErr = errors.New("commit failed")
	service := New(fakeFactory{uow: uow}, &fakeGenerator{})

	if err := service.Process(context.Background(), organizationEvent(EventOrganizationCreated)); err == nil {
		t.Fatal("Process() returned success before commit")
	}
	if uow.commitCalls != 1 || uow.rollbackCalls != 1 {
		t.Fatalf("commit/rollback = %d/%d", uow.commitCalls, uow.rollbackCalls)
	}
}

func TestServiceMembershipConflictRollsBackSavepointBeforeRead(t *testing.T) {
	uow := newFakeUnitOfWork()
	uow.organizationFound = true
	uow.organization = Organization{ID: testOrganizationID, Status: "active"}
	uow.insertMembershipErr = &UniqueConstraintError{
		Constraint: ConstraintActiveOrganizationMember,
		Cause:      errors.New("duplicate key"),
	}
	uow.activeFound = true
	uow.activeMembership = Membership{ClerkMembershipID: "mem-existing", Status: "active"}
	service := New(fakeFactory{uow: uow}, &fakeGenerator{ids: []uuid.UUID{testSecondID}})

	err := service.Process(context.Background(), membershipEvent(EventMembershipCreated, nil))
	if !errors.Is(err, ErrActiveMembershipConflict) {
		t.Fatalf("Process() error = %v", err)
	}
	want := []string{"savepoint", "insert_membership", "rollback_savepoint", "release_savepoint", "get_active_membership"}
	if !containsOrdered(uow.sequence, want) {
		t.Fatalf("sequence = %#v, want ordered %#v", uow.sequence, want)
	}
	if uow.rollbackCalls != 1 || uow.commitCalls != 0 {
		t.Fatalf("commit/rollback = %d/%d", uow.commitCalls, uow.rollbackCalls)
	}
}

func TestServiceUnrelatedUniqueViolationIsNotIdempotent(t *testing.T) {
	uow := newFakeUnitOfWork()
	uow.organizationFound = true
	uow.organization = Organization{ID: testOrganizationID, Status: "active"}
	uow.insertMembershipErr = &UniqueConstraintError{Constraint: "memberships_pkey", Cause: errors.New("duplicate key")}
	service := New(fakeFactory{uow: uow}, &fakeGenerator{ids: []uuid.UUID{testMembershipID}})

	if err := service.Process(context.Background(), membershipEvent(EventMembershipCreated, nil)); err == nil {
		t.Fatal("Process() error = nil")
	}
	if uow.getActiveCalls != 0 || uow.getMembershipCalls != 1 {
		t.Fatalf("unexpected conflict follow-up reads: active=%d membership=%d", uow.getActiveCalls, uow.getMembershipCalls)
	}
	want := []string{"savepoint", "insert_membership", "rollback_savepoint", "release_savepoint", "rollback"}
	if !containsOrdered(uow.sequence, want) {
		t.Fatalf("sequence = %#v, want ordered %#v", uow.sequence, want)
	}
}

func organizationEvent(eventType string) Event {
	return Event{
		EventID: "evt-org", Type: eventType, AggregateType: AggregateOrganization,
		AggregateID: "org-1", ClerkOrganizationID: "org-1", OccurredAt: time.Unix(10, 0),
	}
}

func membershipEvent(eventType string, role *string) Event {
	return Event{
		EventID: "evt-membership", Type: eventType, AggregateType: AggregateMembership,
		AggregateID: "mem-1", ClerkOrganizationID: "org-1", OccurredAt: time.Unix(10, 0),
		Membership: MembershipProjection{
			ClerkMembershipID: "mem-1", ClerkUserID: "user-1", ClerkRole: role,
		},
	}
}

func containsOrdered(sequence, wanted []string) bool {
	position := 0
	for _, item := range sequence {
		if position < len(wanted) && item == wanted[position] {
			position++
		}
	}
	return position == len(wanted)
}
