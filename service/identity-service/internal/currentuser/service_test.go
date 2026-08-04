package currentuser

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type readerFunc func(context.Context, string) (User, bool, error)

func (f readerFunc) GetCurrentUserByClerkUserID(ctx context.Context, clerkUserID string) (User, bool, error) {
	return f(ctx, clerkUserID)
}

func TestServiceReturnsActiveCurrentUserWithoutMutation(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("0198f3be-bf6f-7b0a-8a25-f8433567e0c1")
	createdAt := time.Date(2026, time.August, 3, 10, 11, 12, 0, time.FixedZone("ICT", 7*60*60))
	updatedAt := createdAt.Add(time.Hour)
	stored := User{
		ID:           id,
		ClerkUserID:  "user_active",
		PrimaryEmail: stringPointer("developer@example.com"),
		IDUser:       "bw012303082645",
		Status:       "active",
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}

	var calls int
	service := New(readerFunc(func(_ context.Context, clerkUserID string) (User, bool, error) {
		calls++
		if clerkUserID != stored.ClerkUserID {
			t.Fatalf("clerkUserID = %q", clerkUserID)
		}
		return stored, true, nil
	}))

	got, err := service.Get(context.Background(), stored.ClerkUserID)
	if err != nil {
		t.Fatalf("get current user: %v", err)
	}
	if calls != 1 {
		t.Fatalf("reader calls = %d", calls)
	}
	if got.ID != stored.ID || got.IDUser != stored.IDUser || got.Status != "active" {
		t.Fatalf("identity fields changed: %+v", got)
	}
	if got.CreatedAt.Location() != time.UTC || got.UpdatedAt.Location() != time.UTC {
		t.Fatalf("timestamps are not UTC: created=%s updated=%s", got.CreatedAt.Location(), got.UpdatedAt.Location())
	}
	if stored.CreatedAt.Location() == time.UTC || stored.UpdatedAt.Location() == time.UTC {
		t.Fatal("stored fixture was mutated")
	}
}

func TestServiceEnforcesLocalLifecycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		user      User
		found     bool
		readError error
		wantError error
	}{
		{name: "identity not ready", found: false, wantError: ErrIdentityNotReady},
		{name: "disabled", user: User{Status: "disabled"}, found: true, wantError: ErrAccountDisabled},
		{name: "deleted", user: User{Status: "deleted"}, found: true, wantError: ErrAccountDeleted},
		{name: "database failure", readError: errors.New("database unavailable"), wantError: errors.New("database unavailable")},
		{name: "unsupported status", user: User{Status: "pending"}, found: true, wantError: errors.New("local account has an unsupported status")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			service := New(readerFunc(func(context.Context, string) (User, bool, error) {
				return tt.user, tt.found, tt.readError
			}))
			_, err := service.Get(context.Background(), "user_test")
			if tt.readError != nil {
				if err == nil || err.Error() != tt.readError.Error() {
					t.Fatalf("error = %v, want %v", err, tt.readError)
				}
				return
			}
			if !errors.Is(err, tt.wantError) && (err == nil || err.Error() != tt.wantError.Error()) {
				t.Fatalf("error = %v, want %v", err, tt.wantError)
			}
		})
	}
}

func stringPointer(value string) *string {
	return &value
}
