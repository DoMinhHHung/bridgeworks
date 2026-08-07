package clerkorganizationadmin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/membershipadmin"
	"github.com/google/uuid"
)

func TestCreateInvitationUsesProviderMemberRoleAndOpaqueLocalIntent(t *testing.T) {
	intentID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000a1")
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/organizations/org_test/invitations" || r.Method != http.MethodPost {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Fatal("missing Clerk authorization")
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"inv_test","object":"organization_invitation","email_address":"person@company.example","role":"org:member","status":"pending","organization_id":"org_test","created_at":1,"updated_at":1}`))
	}))
	defer server.Close()

	client, err := New("sk_test_private", server.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if err := client.CreateInvitation(context.Background(), membershipadmin.InvitationProviderRequest{
		ClerkOrganizationID: "org_test",
		ClerkInviterUserID:  "user_inviter",
		EmailAddress:        "person@company.example",
		InvitationIntentID:  intentID,
	}); err != nil {
		t.Fatalf("CreateInvitation() error = %v", err)
	}
	if captured["role"] != "org:member" || captured["email_address"] != "person@company.example" || captured["inviter_user_id"] != "user_inviter" {
		t.Fatalf("provider payload = %#v", captured)
	}
	metadata, ok := captured["public_metadata"].(map[string]any)
	if !ok || metadata["bridgeworks_invitation_id"] != intentID.String() {
		t.Fatalf("public metadata = %#v", captured["public_metadata"])
	}
	if _, exists := metadata["application_role"]; exists {
		t.Fatal("local application role leaked into provider metadata")
	}
}

func TestDeleteMembershipUsesOrganizationAndUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/organizations/org_test/memberships/user_target" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"mem_test","object":"organization_membership","role":"org:member"}`))
	}))
	defer server.Close()
	client, err := New("sk_test_private", server.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if err := client.DeleteMembership(context.Background(), membershipadmin.MembershipDeleteProviderRequest{
		ClerkOrganizationID: "org_test",
		ClerkUserID:         "user_target",
	}); err != nil {
		t.Fatalf("DeleteMembership() error = %v", err)
	}
}

func TestProviderErrorsAreSanitized(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		status     int
		createWant error
		deleteWant error
	}{
		{name: "conflict", status: http.StatusConflict, createWant: membershipadmin.ProviderErrConflict, deleteWant: membershipadmin.ProviderErrConflict},
		{name: "rate limit", status: http.StatusTooManyRequests, createWant: membershipadmin.ProviderErrUnavailable, deleteWant: membershipadmin.ProviderErrUnavailable},
		{name: "not found", status: http.StatusNotFound, createWant: membershipadmin.ProviderErrRejected, deleteWant: membershipadmin.ProviderErrNotFound},
		{name: "server", status: http.StatusBadGateway, createWant: membershipadmin.ProviderErrUnavailable, deleteWant: membershipadmin.ProviderErrUnavailable},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(`{"errors":[{"message":"provider-secret-diagnostic"}]}`))
			}))
			defer server.Close()
			client, err := New("sk_test_private", server.URL, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			createErr := client.CreateInvitation(context.Background(), membershipadmin.InvitationProviderRequest{
				ClerkOrganizationID: "org_test", ClerkInviterUserID: "user_inviter",
				EmailAddress: "person@company.example", InvitationIntentID: uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000a2"),
			})
			if !errors.Is(createErr, testCase.createWant) || strings.Contains(createErr.Error(), "provider-secret-diagnostic") {
				t.Fatalf("CreateInvitation() error = %v", createErr)
			}
			deleteErr := client.DeleteMembership(context.Background(), membershipadmin.MembershipDeleteProviderRequest{
				ClerkOrganizationID: "org_test", ClerkUserID: "user_target",
			})
			if !errors.Is(deleteErr, testCase.deleteWant) || strings.Contains(deleteErr.Error(), "provider-secret-diagnostic") {
				t.Fatalf("DeleteMembership() error = %v", deleteErr)
			}
		})
	}
}

func TestProviderTimeoutIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := New("sk_test_private", server.URL, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	err = client.DeleteMembership(context.Background(), membershipadmin.MembershipDeleteProviderRequest{
		ClerkOrganizationID: "org_test", ClerkUserID: "user_target",
	})
	if !errors.Is(err, membershipadmin.ProviderErrUnavailable) {
		t.Fatalf("DeleteMembership() error = %v", err)
	}
}
