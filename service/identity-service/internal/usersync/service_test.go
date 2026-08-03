package usersync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/clerkwebhook"
	"github.com/google/uuid"
)

var (
	errCollision = errors.New("id_user collision")
	errDatabase  = errors.New("database unavailable")
)

func TestProcessDuplicateAndStaleEventsDoNotMutateUser(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		inboxResult bool
		stale       bool
	}{
		{name: "duplicate", inboxResult: false},
		{name: "stale", inboxResult: true, stale: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			tx := newFakeTransaction()
			tx.inboxResult = test.inboxResult
			tx.stale = test.stale
			repository := &fakeRepository{transaction: tx}
			service := New(repository, &fakeUUIDGenerator{}, &fakeIDUserGenerator{})

			if err := service.Process(context.Background(), testEvent(clerkwebhook.EventUserUpdated)); err != nil {
				t.Fatalf("process: %v", err)
			}
			if !tx.committed {
				t.Fatal("transaction was not committed")
			}
			if tx.getCalls != 0 || tx.insertCalls != 0 || tx.updateCalls != 0 || tx.deleteCalls != 0 {
				t.Fatalf("user was mutated: %+v", tx)
			}
		})
	}
}

func TestProcessCreateAndUpdateInsertMissingActiveUser(t *testing.T) {
	t.Parallel()

	for _, eventType := range []string{clerkwebhook.EventUserCreated, clerkwebhook.EventUserUpdated} {
		eventType := eventType
		t.Run(eventType, func(t *testing.T) {
			t.Parallel()
			tx := newFakeTransaction()
			fixedUUID := uuid.MustParse("018f05d2-89f7-7cc2-98c8-a53ee28f2f71")
			uuidGenerator := &fakeUUIDGenerator{value: fixedUUID}
			idGenerator := &fakeIDUserGenerator{values: []string{"bw012303082645"}}
			service := New(&fakeRepository{transaction: tx}, uuidGenerator, idGenerator)

			if err := service.Process(context.Background(), testEvent(eventType)); err != nil {
				t.Fatalf("process: %v", err)
			}
			if len(tx.insertedUsers) != 1 {
				t.Fatalf("inserted users = %d", len(tx.insertedUsers))
			}
			inserted := tx.insertedUsers[0]
			if inserted.ID != fixedUUID || inserted.IDUser != "bw012303082645" || inserted.Status != "active" {
				t.Fatalf("unexpected inserted user: %+v", inserted)
			}
			if inserted.PrimaryEmail == nil || *inserted.PrimaryEmail != "verified@example.test" {
				t.Fatalf("primary email = %#v", inserted.PrimaryEmail)
			}
			if uuidGenerator.calls != 1 || idGenerator.calls != 1 {
				t.Fatalf("generator calls: uuid=%d id_user=%d", uuidGenerator.calls, idGenerator.calls)
			}
		})
	}
}

func TestProcessUpdatePreservesDisabledAndDeletedIdentityFields(t *testing.T) {
	t.Parallel()

	for _, status := range []string{"disabled", "deleted"} {
		status := status
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			original := User{
				ID:          uuid.MustParse("018f05d2-89f7-7cc2-98c8-a53ee28f2f71"),
				ClerkUserID: "user_test",
				IDUser:      "bw111103082622",
				Status:      status,
			}
			tx := newFakeTransaction()
			tx.user = &original
			uuidGenerator := &fakeUUIDGenerator{}
			idGenerator := &fakeIDUserGenerator{}
			service := New(&fakeRepository{transaction: tx}, uuidGenerator, idGenerator)

			if err := service.Process(context.Background(), testEvent(clerkwebhook.EventUserUpdated)); err != nil {
				t.Fatalf("process: %v", err)
			}
			if tx.user.Status != status || tx.user.ID != original.ID || tx.user.IDUser != original.IDUser {
				t.Fatalf("owned fields changed: before=%+v after=%+v", original, *tx.user)
			}
			if tx.user.PrimaryEmail == nil || *tx.user.PrimaryEmail != "verified@example.test" {
				t.Fatalf("primary email was not updated: %#v", tx.user.PrimaryEmail)
			}
			if uuidGenerator.calls != 0 || idGenerator.calls != 0 {
				t.Fatal("existing user generated replacement identity fields")
			}
		})
	}
}

func TestProcessDeleteClearsEmailAndPreservesIdentityFields(t *testing.T) {
	t.Parallel()

	email := "old@example.test"
	original := User{
		ID:           uuid.MustParse("018f05d2-89f7-7cc2-98c8-a53ee28f2f71"),
		ClerkUserID:  "user_test",
		PrimaryEmail: &email,
		IDUser:       "bw111103082622",
		Status:       "disabled",
	}
	tx := newFakeTransaction()
	tx.user = &original
	service := New(&fakeRepository{transaction: tx}, &fakeUUIDGenerator{}, &fakeIDUserGenerator{})

	if err := service.Process(context.Background(), testEvent(clerkwebhook.EventUserDeleted)); err != nil {
		t.Fatalf("process: %v", err)
	}
	if tx.user.Status != "deleted" || tx.user.PrimaryEmail != nil {
		t.Fatalf("delete projection = %+v", *tx.user)
	}
	if tx.user.ID != original.ID || tx.user.IDUser != original.IDUser {
		t.Fatal("delete changed immutable identity fields")
	}
}

func TestProcessDeleteCreatesTombstone(t *testing.T) {
	t.Parallel()

	tx := newFakeTransaction()
	service := New(
		&fakeRepository{transaction: tx},
		&fakeUUIDGenerator{value: uuid.MustParse("018f05d2-89f7-7cc2-98c8-a53ee28f2f71")},
		&fakeIDUserGenerator{values: []string{"bw012303082645"}},
	)

	if err := service.Process(context.Background(), testEvent(clerkwebhook.EventUserDeleted)); err != nil {
		t.Fatalf("process: %v", err)
	}
	if len(tx.insertedUsers) != 1 || tx.insertedUsers[0].Status != "deleted" || tx.insertedUsers[0].PrimaryEmail != nil {
		t.Fatalf("unexpected tombstone: %+v", tx.insertedUsers)
	}
}

func TestIDUserCollisionRetriesOnlyExactCollision(t *testing.T) {
	t.Parallel()

	t.Run("success after collision", func(t *testing.T) {
		t.Parallel()
		tx := newFakeTransaction()
		tx.insertErrors = []error{errCollision, nil}
		uuidGenerator := &fakeUUIDGenerator{value: uuid.MustParse("018f05d2-89f7-7cc2-98c8-a53ee28f2f71")}
		idGenerator := &fakeIDUserGenerator{values: []string{"bw000003082600", "bw000103082601"}}
		service := New(&fakeRepository{transaction: tx}, uuidGenerator, idGenerator)

		if err := service.Process(context.Background(), testEvent(clerkwebhook.EventUserCreated)); err != nil {
			t.Fatalf("process: %v", err)
		}
		if tx.insertCalls != 2 || uuidGenerator.calls != 1 || idGenerator.calls != 2 {
			t.Fatalf("unexpected retry counts: inserts=%d uuid=%d id=%d", tx.insertCalls, uuidGenerator.calls, idGenerator.calls)
		}
	})

	t.Run("fails after five collisions", func(t *testing.T) {
		t.Parallel()
		tx := newFakeTransaction()
		tx.insertErrors = []error{errCollision, errCollision, errCollision, errCollision, errCollision}
		idGenerator := &fakeIDUserGenerator{values: []string{
			"bw000003082600", "bw000103082601", "bw000203082602", "bw000303082603", "bw000403082604",
		}}
		service := New(&fakeRepository{transaction: tx}, &fakeUUIDGenerator{}, idGenerator)

		err := service.Process(context.Background(), testEvent(clerkwebhook.EventUserCreated))
		if !errors.Is(err, ErrIDUserCollisionExhausted) {
			t.Fatalf("error = %v", err)
		}
		if tx.insertCalls != 5 || idGenerator.calls != 5 {
			t.Fatalf("unexpected attempts: inserts=%d id=%d", tx.insertCalls, idGenerator.calls)
		}
	})

	t.Run("unrelated database error is not retried", func(t *testing.T) {
		t.Parallel()
		tx := newFakeTransaction()
		tx.insertErrors = []error{errDatabase}
		idGenerator := &fakeIDUserGenerator{values: []string{"bw000003082600"}}
		service := New(&fakeRepository{transaction: tx}, &fakeUUIDGenerator{}, idGenerator)

		err := service.Process(context.Background(), testEvent(clerkwebhook.EventUserCreated))
		if !errors.Is(err, errDatabase) || tx.insertCalls != 1 || idGenerator.calls != 1 {
			t.Fatalf("unexpected result: error=%v inserts=%d ids=%d", err, tx.insertCalls, idGenerator.calls)
		}
	})
}

func TestProcessingFailureRollsBackInboxEvent(t *testing.T) {
	t.Parallel()

	tx := newFakeTransaction()
	tx.getError = errDatabase
	service := New(&fakeRepository{transaction: tx}, &fakeUUIDGenerator{}, &fakeIDUserGenerator{})

	if err := service.Process(context.Background(), testEvent(clerkwebhook.EventUserCreated)); !errors.Is(err, errDatabase) {
		t.Fatalf("error = %v", err)
	}
	if tx.inboxPersisted || !tx.rolledBack || tx.committed {
		t.Fatalf("transaction state: committed=%v rolledBack=%v inbox=%v", tx.committed, tx.rolledBack, tx.inboxPersisted)
	}
}

func testEvent(eventType string) clerkwebhook.Event {
	email := "verified@example.test"
	return clerkwebhook.Event{
		EventID:      "msg_test",
		Type:         eventType,
		ClerkUserID:  "user_test",
		OccurredAt:   time.Date(2026, time.August, 3, 13, 0, 0, 0, time.UTC),
		PrimaryEmail: &email,
		Supported:    true,
	}
}

type fakeRepository struct {
	transaction *fakeTransaction
}

func (r *fakeRepository) Begin(context.Context) (Transaction, error) {
	return r.transaction, nil
}

func (*fakeRepository) IsIDUserCollision(err error) bool {
	return errors.Is(err, errCollision)
}

type fakeTransaction struct {
	inboxResult    bool
	stale          bool
	user           *User
	getError       error
	insertErrors   []error
	insertedUsers  []User
	inboxStaged    bool
	inboxPersisted bool
	committed      bool
	rolledBack     bool
	getCalls       int
	insertCalls    int
	updateCalls    int
	deleteCalls    int
}

func newFakeTransaction() *fakeTransaction {
	return &fakeTransaction{inboxResult: true}
}

func (*fakeTransaction) LockClerkUser(context.Context, string) error { return nil }

func (t *fakeTransaction) InsertInboxEvent(context.Context, clerkwebhook.Event) (bool, error) {
	if t.inboxResult {
		t.inboxStaged = true
	}
	return t.inboxResult, nil
}

func (t *fakeTransaction) HasSupersedingEvent(context.Context, clerkwebhook.Event) (bool, error) {
	return t.stale, nil
}

func (t *fakeTransaction) GetUserByClerkID(context.Context, string) (User, bool, error) {
	t.getCalls++
	if t.getError != nil {
		return User{}, false, t.getError
	}
	if t.user == nil {
		return User{}, false, nil
	}
	return *t.user, true, nil
}

func (t *fakeTransaction) InsertUser(_ context.Context, user User) error {
	t.insertCalls++
	if len(t.insertErrors) >= t.insertCalls && t.insertErrors[t.insertCalls-1] != nil {
		return t.insertErrors[t.insertCalls-1]
	}
	t.insertedUsers = append(t.insertedUsers, user)
	t.user = &t.insertedUsers[len(t.insertedUsers)-1]
	return nil
}

func (t *fakeTransaction) UpdatePrimaryEmail(_ context.Context, _ string, email *string) error {
	t.updateCalls++
	t.user.PrimaryEmail = email
	return nil
}

func (t *fakeTransaction) MarkDeleted(context.Context, string) error {
	t.deleteCalls++
	t.user.Status = "deleted"
	t.user.PrimaryEmail = nil
	return nil
}

func (t *fakeTransaction) Commit(context.Context) error {
	t.committed = true
	t.inboxPersisted = t.inboxStaged
	return nil
}

func (t *fakeTransaction) Rollback(context.Context) error {
	if t.committed {
		return nil
	}
	t.rolledBack = true
	t.inboxStaged = false
	t.inboxPersisted = false
	return nil
}

type fakeUUIDGenerator struct {
	value uuid.UUID
	err   error
	calls int
}

func (g *fakeUUIDGenerator) New() (uuid.UUID, error) {
	g.calls++
	if g.err != nil {
		return uuid.Nil, g.err
	}
	if g.value == uuid.Nil {
		g.value = uuid.MustParse("018f05d2-89f7-7cc2-98c8-a53ee28f2f71")
	}
	return g.value, nil
}

type fakeIDUserGenerator struct {
	values []string
	err    error
	calls  int
}

func (g *fakeIDUserGenerator) Generate() (string, error) {
	g.calls++
	if g.err != nil {
		return "", g.err
	}
	if len(g.values) < g.calls {
		return "bw999903082699", nil
	}
	return g.values[g.calls-1], nil
}
