package config

import (
	"strings"
	"testing"
	"time"
)

func validEnvironment() map[string]string {
	return map[string]string{
		"DATABASE_URL":             "postgres://user:password@organization-postgres:5432/db",
		"CLERK_JWT_KEY":            "public-key",
		"CLERK_ISSUER":             "https://clerk.example.test",
		"CLERK_AUTHORIZED_PARTIES": "http://localhost:3000,https://app.example.test",
		"CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET": "whsec_test",
	}
}
func lookup(values map[string]string) lookupEnvFunc {
	return func(key string) (string, bool) { value, ok := values[key]; return value, ok }
}
func TestLoadValid(t *testing.T) {
	cfg, err := load(lookup(validEnvironment()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IdentityServiceURL != defaultIdentityServiceURL || cfg.IdentityRequestTimeout != 2*time.Second {
		t.Fatalf("identity defaults unexpected")
	}
	if cfg.DatabaseMaxConns != 5 || cfg.DatabaseMinConns != 0 {
		t.Fatalf("pool config unexpected")
	}
}
func TestLoadRejectsWebhookTimeoutBounds(t *testing.T) {
	for _, value := range []string{"9s", "15s"} {
		env := validEnvironment()
		env["CLERK_ORGANIZATION_WEBHOOK_PROCESS_TIMEOUT"] = value
		if _, err := load(lookup(env)); err == nil {
			t.Fatalf("expected %s rejection", value)
		}
	}
}
func TestLoadRejectsIdentityTimeoutAndURL(t *testing.T) {
	env := validEnvironment()
	env["IDENTITY_SERVICE_REQUEST_TIMEOUT"] = "6s"
	if _, err := load(lookup(env)); err == nil {
		t.Fatal("expected timeout rejection")
	}
	env = validEnvironment()
	env["IDENTITY_SERVICE_URL"] = "http://public.example.test"
	if _, err := load(lookup(env)); err == nil {
		t.Fatal("expected public HTTP rejection")
	}
}
func TestLoadRejectsAuthorizedPartyErrors(t *testing.T) {
	for _, value := range []string{"http://localhost:3000,", "http://localhost:3000,http://localhost:3000", "http://example.test"} {
		env := validEnvironment()
		env["CLERK_AUTHORIZED_PARTIES"] = value
		if _, err := load(lookup(env)); err == nil {
			t.Fatalf("expected party rejection for %q", value)
		}
	}
}
func TestConfigErrorsDoNotEchoSecrets(t *testing.T) {
	env := validEnvironment()
	secret := "very-sensitive-webhook-secret"
	env["CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET"] = secret
	env["IDENTITY_SERVICE_REQUEST_TIMEOUT"] = "invalid"
	_, err := load(lookup(env))
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), env["DATABASE_URL"]) {
		t.Fatal("config error leaked secret")
	}
}
