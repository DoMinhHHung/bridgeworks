package organizationsync

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDecodeOrganizationEvent(t *testing.T) {
	payload := []byte(`{"type":"organization.created","timestamp":1785744000000,"data":{"id":" org_123 ","name":" Acme ","slug":" acme "}}`)
	event, supported, err := Decode("msg_1", payload)
	if err != nil || !supported {
		t.Fatalf("Decode() = supported %v, err %v", supported, err)
	}
	if event.ClerkOrganizationID != "org_123" || event.AggregateType != AggregateOrganization {
		t.Fatalf("unexpected event: %#v", event)
	}
	if event.Organization.Name == nil || *event.Organization.Name != "Acme" {
		t.Fatalf("name not normalized")
	}
	if event.Organization.Slug == nil || *event.Organization.Slug != "acme" {
		t.Fatalf("slug not normalized")
	}
	if !event.OccurredAt.Equal(time.UnixMilli(1785744000000).UTC()) {
		t.Fatalf("timestamp mismatch")
	}
}

func TestDecodeMembershipEventNarrowShape(t *testing.T) {
	payload := []byte(`{"type":"organizationMembership.created","timestamp":1785744000000,"data":{"id":"mem_123","organization":{"id":"org_123","name":"ignored"},"public_user_data":{"user_id":"user_123","identifier":"secret@example.test"},"role":" org:admin ","private_metadata":{"ignored":true}}}`)
	event, supported, err := Decode("msg_2", payload)
	if err != nil || !supported {
		t.Fatalf("Decode() err = %v", err)
	}
	if event.Membership.ClerkMembershipID != "mem_123" || event.Membership.ClerkUserID != "user_123" {
		t.Fatalf("unexpected membership: %#v", event.Membership)
	}
	if event.Membership.ClerkRole == nil || *event.Membership.ClerkRole != ClerkRoleAdmin {
		t.Fatalf("role not normalized")
	}
}

func TestDecodeMembershipInvitationIntentMarker(t *testing.T) {
	intentID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000091")
	payload := []byte(`{"type":"organizationMembership.created","timestamp":1785744000000,"data":{"id":"mem_123","organization":{"id":"org_123"},"public_user_data":{"user_id":"user_123"},"role":"org:member","public_metadata":{"bridgeworks_invitation_id":"018f0c76-8f6c-7cc4-8000-000000000091","application_role":"owner","email":"ignored@example.test"}}}`)
	event, supported, err := Decode("msg_invitation", payload)
	if err != nil || !supported {
		t.Fatalf("Decode() err = %v", err)
	}
	if event.Membership.InvitationIntent == nil || *event.Membership.InvitationIntent != intentID {
		t.Fatalf("invitation intent = %#v", event.Membership.InvitationIntent)
	}
}

func TestDecodeIgnoresInvalidInvitationIntentMarker(t *testing.T) {
	for _, value := range []string{"not-a-uuid", "550e8400-e29b-41d4-a716-446655440000"} {
		payload := []byte(`{"type":"organizationMembership.created","timestamp":1785744000000,"data":{"id":"mem_123","organization":{"id":"org_123"},"public_user_data":{"user_id":"user_123"},"public_metadata":{"bridgeworks_invitation_id":"` + value + `"}}}`)
		event, supported, err := Decode("msg_invalid_intent", payload)
		if err != nil || !supported {
			t.Fatalf("Decode() err = %v", err)
		}
		if event.Membership.InvitationIntent != nil {
			t.Fatalf("invalid marker %q was trusted", value)
		}
	}
}

func TestDecodeRejectsMissingMembershipStructure(t *testing.T) {
	cases := []string{
		`{"type":"organizationMembership.created","timestamp":1,"data":{"id":"mem","public_user_data":{"user_id":"user"}}}`,
		`{"type":"organizationMembership.created","timestamp":1,"data":{"id":"mem","organization":{"id":"org"}}}`,
		`{"type":"organizationMembership.created","timestamp":1,"data":{"organization":{"id":"org"},"public_user_data":{"user_id":"user"}}}`,
	}
	for _, payload := range cases {
		if _, _, err := Decode("msg", []byte(payload)); err == nil {
			t.Fatalf("expected error for %s", payload)
		}
	}
}

func TestDecodeBlankOptionalOrganizationFields(t *testing.T) {
	payload := []byte(`{"type":"organization.updated","timestamp":1785744000000,"data":{"id":"org_123","name":"   ","slug":""}}`)
	event, supported, err := Decode("msg", payload)
	if err != nil || !supported {
		t.Fatalf("Decode() supported=%v err=%v", supported, err)
	}
	if event.Organization.Name != nil || event.Organization.Slug != nil {
		t.Fatalf("blank optional fields must map to nil: %#v", event.Organization)
	}
}

func TestDecodeUnsupported(t *testing.T) {
	_, supported, err := Decode("msg", []byte(`{"type":"session.created","timestamp":1,"data":{}}`))
	if err != nil || supported {
		t.Fatalf("supported=%v err=%v", supported, err)
	}
}

func TestSnakeCaseMembershipEventIsUnsupported(t *testing.T) {
	_, supported, err := Decode("msg", []byte(`{"type":"organization_membership.created","timestamp":1,"data":{}}`))
	if err != nil || supported {
		t.Fatalf("supported=%v err=%v", supported, err)
	}
}

func TestOrdering(t *testing.T) {
	base := time.Unix(100, 0).UTC()
	latest := Event{EventID: "msg_b", Type: EventOrganizationUpdated, OccurredAt: base}
	if !IsStale(Event{EventID: "msg_z", Type: EventOrganizationCreated, OccurredAt: base}, latest) {
		t.Fatal("create should lose to update")
	}
	if IsStale(Event{EventID: "msg_a", Type: EventOrganizationDeleted, OccurredAt: base}, latest) {
		t.Fatal("delete should beat update")
	}
	if !IsStale(Event{EventID: "msg_a", Type: EventOrganizationUpdated, OccurredAt: base}, latest) {
		t.Fatal("lower lexical id should be stale")
	}
	if IsStale(Event{EventID: "msg_c", Type: EventOrganizationUpdated, OccurredAt: base}, latest) {
		t.Fatal("higher lexical id should win")
	}
	if !IsStale(Event{EventID: "msg_z", Type: EventOrganizationDeleted, OccurredAt: base.Add(-time.Second)}, latest) {
		t.Fatal("older timestamp should be stale")
	}
}

func TestInitialApplicationRole(t *testing.T) {
	admin := ClerkRoleAdmin
	member := "org:member"
	almost := "prefix-org:admin"
	if InitialApplicationRole(&admin) != RoleAdmin {
		t.Fatal("admin mapping failed")
	}
	if InitialApplicationRole(&member) != RoleViewer || InitialApplicationRole(&almost) != RoleViewer || InitialApplicationRole(nil) != RoleViewer {
		t.Fatal("non-admin must map viewer")
	}
}
