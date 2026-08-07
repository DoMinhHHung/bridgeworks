package organizationdomain

import "testing"

func TestCanTransitionVerification(t *testing.T) {
	tests := []struct {
		name string
		from string
		to   string
		want bool
	}{
		{name: "submit unverified", from: VerificationStatusUnverified, to: VerificationStatusPending, want: true},
		{name: "approve pending", from: VerificationStatusPending, to: VerificationStatusVerified, want: true},
		{name: "reject pending", from: VerificationStatusPending, to: VerificationStatusRejected, want: true},
		{name: "resubmit rejected", from: VerificationStatusRejected, to: VerificationStatusPending, want: true},
		{name: "idempotent verified", from: VerificationStatusVerified, to: VerificationStatusVerified, want: true},
		{name: "skip review", from: VerificationStatusUnverified, to: VerificationStatusVerified, want: false},
		{name: "reopen verified without policy", from: VerificationStatusVerified, to: VerificationStatusPending, want: false},
		{name: "unknown source", from: "unknown", to: VerificationStatusPending, want: false},
		{name: "unknown target", from: VerificationStatusPending, to: "unknown", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanTransitionVerification(tt.from, tt.to); got != tt.want {
				t.Fatalf("CanTransitionVerification(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestCanTransitionTrustDoesNotInventPolicy(t *testing.T) {
	if !CanTransitionTrust(TrustStatusUnassessed, TrustStatusUnassessed) {
		t.Fatal("unassessed trust status must allow idempotent persistence")
	}
	if CanTransitionTrust(TrustStatusUnassessed, "trusted") {
		t.Fatal("undefined trust transition must be denied")
	}
}
