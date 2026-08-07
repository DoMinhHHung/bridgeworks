package main

import "testing"

func TestParseCommandAcceptsOnlyPublicIDUserTargeting(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"grant", "revoke", "status"} {
		parsed, err := parseCommand([]string{name, "--id-user", "bw123401012678"})
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if parsed.name != name || parsed.idUser != "bw123401012678" {
			t.Fatalf("parsed = %+v", parsed)
		}
	}
}

func TestParseCommandRejectsAlternativeIdentitySelectorsAndInvalidIDs(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"grant", "--email", "operator@example.test"},
		{"grant", "--clerk-user-id", "user_provider"},
		{"grant", "--id-user", "user_provider"},
		{"grant", "--id-user", "bw1234"},
		{"grant", "--id-user", "bw12340101267x"},
		{"promote", "--id-user", "bw123401012678"},
	} {
		if _, err := parseCommand(args); err == nil {
			t.Fatalf("parseCommand(%#v) expected error", args)
		}
	}
}
