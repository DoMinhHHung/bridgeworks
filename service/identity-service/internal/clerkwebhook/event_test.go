package clerkwebhook

import (
	"fmt"
	"testing"
)

func TestVerifiedPrimaryEmailRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		user   userData
		wanted *string
	}{
		{
			name: "verified matching primary email",
			user: userData{
				PrimaryEmailAddressID: "email_primary",
				EmailAddresses: []emailAddress{
					{ID: "email_other", EmailAddress: "first@example.test", Verification: emailVerification{Status: "verified"}},
					{ID: "email_primary", EmailAddress: "primary@example.test", Verification: emailVerification{Status: "verified"}},
				},
			},
			wanted: stringPointer("primary@example.test"),
		},
		{name: "primary ID missing", user: userData{EmailAddresses: verifiedEmails()}, wanted: nil},
		{name: "matching email missing", user: userData{PrimaryEmailAddressID: "missing", EmailAddresses: verifiedEmails()}, wanted: nil},
		{
			name:   "unverified primary email",
			user:   userData{PrimaryEmailAddressID: "email_primary", EmailAddresses: []emailAddress{{ID: "email_primary", EmailAddress: "primary@example.test", Verification: emailVerification{Status: "unverified"}}}},
			wanted: nil,
		},
		{
			name:   "blank email",
			user:   userData{PrimaryEmailAddressID: "email_primary", EmailAddresses: []emailAddress{{ID: "email_primary", EmailAddress: "  ", Verification: emailVerification{Status: "verified"}}}},
			wanted: nil,
		},
		{
			name:   "first email is not primary",
			user:   userData{PrimaryEmailAddressID: "email_primary", EmailAddresses: []emailAddress{{ID: "email_first", EmailAddress: "first@example.test", Verification: emailVerification{Status: "verified"}}}},
			wanted: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := verifiedPrimaryEmail(tt.user)
			if !sameStringPointer(got, tt.wanted) {
				t.Fatalf("primary email = %#v, want %#v", got, tt.wanted)
			}
		})
	}
}

func TestParseEventValidationAndUnsupportedType(t *testing.T) {
	t.Parallel()

	valid := []byte(`{"type":"user.created","timestamp":1785744000000,"data":{"id":"user_test","primary_email_address_id":"email_primary","email_addresses":[{"id":"email_primary","email_address":"primary@example.test","verification":{"status":"verified"}}]}}`)
	event, err := parseEvent("msg_test", valid)
	if err != nil {
		t.Fatalf("parse event: %v", err)
	}
	if !event.Supported || event.EventID != "msg_test" || event.ClerkUserID != "user_test" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.OccurredAt.Location().String() != "UTC" {
		t.Fatalf("occurred_at location = %s", event.OccurredAt.Location())
	}

	unsupported, err := parseEvent("msg_unknown", []byte(`{"type":"session.created"}`))
	if err != nil {
		t.Fatalf("parse unsupported event: %v", err)
	}
	if unsupported.Supported || unsupported.Type != "session.created" {
		t.Fatalf("unexpected unsupported event: %+v", unsupported)
	}

	invalidPayloads := [][]byte{
		[]byte(`not-json`),
		[]byte(`{"type":"user.created","timestamp":0,"data":{"id":"user_test"}}`),
		[]byte(`{"type":"user.created","timestamp":1785744000000,"data":null}`),
		[]byte(`{"type":"user.created","timestamp":1785744000000,"data":{"id":" "}}`),
		[]byte(`{"type":"user.created","timestamp":1785744000000,"data":{"id":"user_test"}} {}`),
	}
	for index, payload := range invalidPayloads {
		if _, err := parseEvent(fmt.Sprintf("msg_%d", index), payload); err != ErrInvalidWebhook {
			t.Fatalf("payload %d error = %v", index, err)
		}
	}
}

func verifiedEmails() []emailAddress {
	return []emailAddress{{ID: "email_primary", EmailAddress: "primary@example.test", Verification: emailVerification{Status: "verified"}}}
}

func stringPointer(value string) *string { return &value }

func sameStringPointer(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
