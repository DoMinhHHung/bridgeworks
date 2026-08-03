package safeerr

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestWrapSanitizesMessageAndPreservesCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("dial postgres://runtime:super-secret@identity-postgres:5432/identity: raw pgx error")
	wrapped := Wrap("ping postgres during startup", cause)
	if wrapped == nil {
		t.Fatal("expected wrapped error")
	}
	if wrapped.Error() != "ping postgres during startup" {
		t.Fatalf("unexpected public error: %q", wrapped.Error())
	}
	if !errors.Is(wrapped, cause) {
		t.Fatal("wrapped error does not preserve its cause")
	}

	for _, secret := range []string{
		"postgres://",
		"runtime",
		"super-secret",
		"identity-postgres",
		"raw pgx error",
	} {
		if strings.Contains(wrapped.Error(), secret) {
			t.Fatalf("public error leaked %q: %q", secret, wrapped.Error())
		}
	}
}

func TestWrapNilCauseReturnsNil(t *testing.T) {
	t.Parallel()

	if err := Wrap("unused message", nil); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestWrappedErrorDoesNotLeakCauseWhenLogged(t *testing.T) {
	t.Parallel()

	cause := errors.New("postgres://runtime:super-secret@identity-postgres:5432/identity raw pgx error")
	wrapped := Wrap("migration database unavailable", cause)

	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	logger.Error("identity operation failed", "error", wrapped)

	logged := output.String()
	if !strings.Contains(logged, "migration database unavailable") {
		t.Fatalf("sanitized message missing from log: %q", logged)
	}
	for _, secret := range []string{
		"postgres://",
		"runtime",
		"super-secret",
		"identity-postgres",
		"raw pgx error",
	} {
		if strings.Contains(logged, secret) {
			t.Fatalf("log leaked %q: %q", secret, logged)
		}
	}
}
