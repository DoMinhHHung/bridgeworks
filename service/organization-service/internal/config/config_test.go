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
		"CLERK_SECRET_KEY":         "sk_test_local",
		"CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET": "whsec_test",
		"ORGANIZATION_PERSONAL_EMAIL_DOMAINS":       "gmail.com,outlook.com",
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
	if cfg.IdentityServiceURL != defaultIdentityServiceURL ||
		cfg.IdentityRequestTimeout != 2*time.Second ||
		cfg.IdentityServiceAuthMode != IdentityServiceAuthModeNone ||
		cfg.IdentityServiceAudience != "" {
		t.Fatalf("identity defaults unexpected")
	}
	if cfg.ClerkBackendAPIURL != defaultClerkBackendAPIURL || cfg.ClerkBackendAPITimeout != 3*time.Second {
		t.Fatalf("Clerk Backend API defaults unexpected")
	}
	if len(cfg.PersonalEmailDomains) != 2 || cfg.PersonalEmailDomains[0] != "gmail.com" {
		t.Fatalf("personal email policy unexpected: %#v", cfg.PersonalEmailDomains)
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
func TestLoadRejectsClerkBackendConfiguration(t *testing.T) {
	env := validEnvironment()
	delete(env, "CLERK_SECRET_KEY")
	if _, err := load(lookup(env)); err == nil {
		t.Fatal("expected missing Clerk secret rejection")
	}
	env = validEnvironment()
	env["CLERK_BACKEND_API_TIMEOUT"] = "6s"
	if _, err := load(lookup(env)); err == nil {
		t.Fatal("expected Clerk Backend API timeout rejection")
	}
	env = validEnvironment()
	env["CLERK_BACKEND_API_URL"] = "https://api.clerk.com/v1"
	if _, err := load(lookup(env)); err == nil {
		t.Fatal("expected versioned Clerk Backend API path rejection")
	}
	env = validEnvironment()
	env["ORGANIZATION_PERSONAL_EMAIL_DOMAINS"] = "gmail.com,"
	if _, err := load(lookup(env)); err == nil {
		t.Fatal("expected empty personal email domain rejection")
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
func TestLoadIdentityServiceAuth(t *testing.T) {
	env := validEnvironment()
	env["IDENTITY_SERVICE_AUTH_MODE"] = "google-id-token"
	env["IDENTITY_SERVICE_AUDIENCE"] = "https://identity-service.example.run.app"

	cfg, err := load(lookup(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IdentityServiceAuthMode != IdentityServiceAuthModeGoogleIDToken {
		t.Fatalf("auth mode = %q", cfg.IdentityServiceAuthMode)
	}
	if cfg.IdentityServiceAudience != "https://identity-service.example.run.app" {
		t.Fatalf("audience = %q", cfg.IdentityServiceAudience)
	}

	tests := []struct {
		name   string
		mutate func(map[string]string)
	}{
		{
			name: "google mode without audience",
			mutate: func(values map[string]string) {
				values["IDENTITY_SERVICE_AUTH_MODE"] = "google-id-token"
			},
		},
		{
			name: "audience while auth disabled",
			mutate: func(values map[string]string) {
				values["IDENTITY_SERVICE_AUDIENCE"] = "https://identity-service.example.run.app"
			},
		},
		{
			name: "unknown auth mode",
			mutate: func(values map[string]string) {
				values["IDENTITY_SERVICE_AUTH_MODE"] = "static-token"
			},
		},
		{
			name: "audience with path",
			mutate: func(values map[string]string) {
				values["IDENTITY_SERVICE_AUTH_MODE"] = "google-id-token"
				values["IDENTITY_SERVICE_AUDIENCE"] = "https://identity-service.example.run.app/me"
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			values := validEnvironment()
			testCase.mutate(values)

			if _, err := load(lookup(values)); err == nil {
				t.Fatal("expected configuration rejection")
			}
		})
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
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), env["DATABASE_URL"]) || strings.Contains(err.Error(), env["CLERK_SECRET_KEY"]) {
		t.Fatal("config error leaked secret")
	}
}
