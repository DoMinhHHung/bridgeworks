package usersync

import (
	"context"
	"fmt"
	"testing"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/clerkwebhook"
)

func TestProcessInsertsInboxBeforeUserLock(t *testing.T) {
	t.Parallel()

	transaction := &orderedTransaction{inboxInserted: true, userExists: true}
	service := New(
		&orderedRepository{transaction: transaction},
		&fakeUUIDGenerator{},
		&fakeIDUserGenerator{},
	)

	if err := service.Process(context.Background(), testEvent(clerkwebhook.EventUserUpdated)); err != nil {
		t.Fatalf("process: %v", err)
	}

	want := []string{"insert_inbox", "lock_user", "check_stale", "get_user", "update_email", "commit"}
	if fmt.Sprint(transaction.calls) != fmt.Sprint(want) {
		t.Fatalf("calls = %v, want %v", transaction.calls, want)
	}
}

func TestProcessDuplicateCommitsWithoutUserLock(t *testing.T) {
	t.Parallel()

	transaction := &orderedTransaction{inboxInserted: false}
	service := New(
		&orderedRepository{transaction: transaction},
		&fakeUUIDGenerator{},
		&fakeIDUserGenerator{},
	)

	if err := service.Process(context.Background(), testEvent(clerkwebhook.EventUserUpdated)); err != nil {
		t.Fatalf("process: %v", err)
	}

	want := []string{"insert_inbox", "commit"}
	if fmt.Sprint(transaction.calls) != fmt.Sprint(want) {
		t.Fatalf("calls = %v, want %v", transaction.calls, want)
	}
}

type orderedRepository struct {
	transaction *orderedTransaction
}

func (r *orderedRepository) Begin(context.Context) (Transaction, error) {
	return r.transaction, nil
}

func (*orderedRepository) IsIDUserCollision(error) bool { return false }

type orderedTransaction struct {
	calls         []string
	inboxInserted bool
	userExists    bool
	committed     bool
}

func (t *orderedTransaction) LockClerkUser(context.Context, string) error {
	t.calls = append(t.calls, "lock_user")
	return nil
}

func (t *orderedTransaction) InsertInboxEvent(context.Context, clerkwebhook.Event) (bool, error) {
	t.calls = append(t.calls, "insert_inbox")
	return t.inboxInserted, nil
}

func (t *orderedTransaction) HasSupersedingEvent(context.Context, clerkwebhook.Event) (bool, error) {
	t.calls = append(t.calls, "check_stale")
	return false, nil
}

func (t *orderedTransaction) GetUserByClerkID(context.Context, string) (User, bool, error) {
	t.calls = append(t.calls, "get_user")
	return User{Status: "active"}, t.userExists, nil
}

func (t *orderedTransaction) InsertUser(context.Context, User) error {
	t.calls = append(t.calls, "insert_user")
	return nil
}

func (t *orderedTransaction) UpdatePrimaryEmail(context.Context, string, *string) error {
	t.calls = append(t.calls, "update_email")
	return nil
}

func (t *orderedTransaction) MarkDeleted(context.Context, string) error {
	t.calls = append(t.calls, "mark_deleted")
	return nil
}

func (t *orderedTransaction) Commit(context.Context) error {
	t.calls = append(t.calls, "commit")
	t.committed = true
	return nil
}

func (t *orderedTransaction) Rollback(context.Context) error {
	if !t.committed {
		t.calls = append(t.calls, "rollback")
	}
	return nil
}
