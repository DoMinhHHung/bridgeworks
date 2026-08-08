package organizationaudit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
	"github.com/google/uuid"
)

type fakeAuditReader struct {
	organizationID uuid.UUID
	calls          int
}

func (f *fakeAuditReader) ListOrganizationAuditEvents(
	_ context.Context,
	organizationID uuid.UUID,
	_ time.Time,
	_ uuid.UUID,
	_ int32,
) ([]Record, error) {
	f.calls++
	f.organizationID = organizationID
	return []Record{{
		ID:         uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000b1"),
		EventType:  EventOrganizationVerificationReviewed,
		ActorKind:  ActorPlatformAdmin,
		OccurredAt: time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC),
	}}, nil
}

func TestAuditReadPermissionMatrix(t *testing.T) {
	organizationID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000b2")
	for _, testCase := range []struct {
		role    string
		allowed bool
	}{
		{role: "owner", allowed: true},
		{role: "admin", allowed: true},
		{role: "viewer", allowed: false},
		{role: "recruiter", allowed: false},
		{role: "delivery_manager", allowed: false},
	} {
		t.Run(testCase.role, func(t *testing.T) {
			permissions := []string(nil)
			if testCase.allowed {
				permissions = []string{authorization.PermissionOrganizationAuditRead}
			}
			actor := authorization.NewActorContext(uuid.New(), organizationID, uuid.New(), testCase.role, permissions)
			reader := &fakeAuditReader{}
			_, err := New(reader).List(context.Background(), actor, 20, "")
			if testCase.allowed {
				if err != nil {
					t.Fatalf("List() error = %v", err)
				}
				if reader.calls != 1 || reader.organizationID != organizationID {
					t.Fatalf("reader calls=%d organization=%s", reader.calls, reader.organizationID)
				}
				return
			}
			if !errors.Is(err, currentorganization.ErrPermissionDenied) {
				t.Fatalf("List() error = %v", err)
			}
			if reader.calls != 0 {
				t.Fatalf("unauthorized reader calls = %d", reader.calls)
			}
		})
	}
}
