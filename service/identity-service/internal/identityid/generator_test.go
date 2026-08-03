package identityid

import (
	"bytes"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUUIDV7Generator(t *testing.T) {
	t.Parallel()

	value, err := (UUIDV7Generator{}).New()
	if err != nil {
		t.Fatalf("generate UUIDv7: %v", err)
	}
	if value.Version() != uuid.Version(7) {
		t.Fatalf("UUID version = %d", value.Version())
	}
}

func TestIDUserFormatTimezoneAndLeadingZeros(t *testing.T) {
	t.Parallel()

	generator, err := New(
		func() time.Time { return time.Date(2026, time.August, 2, 18, 0, 0, 0, time.UTC) },
		bytes.NewReader([]byte{0, 1, 2, 3, 4, 5}),
	)
	if err != nil {
		t.Fatalf("new generator: %v", err)
	}

	value, err := generator.Generate()
	if err != nil {
		t.Fatalf("generate id_user: %v", err)
	}
	if value != "bw012303082645" {
		t.Fatalf("id_user = %q", value)
	}
	if !regexp.MustCompile(`^bw[0-9]{12}$`).MatchString(value) {
		t.Fatalf("invalid id_user format: %q", value)
	}
	if len(value) != 14 {
		t.Fatalf("id_user length = %d", len(value))
	}
}

func TestIDUserRejectsModuloBiasBytes(t *testing.T) {
	t.Parallel()

	generator, err := New(
		func() time.Time { return time.Date(2026, time.August, 3, 0, 0, 0, 0, time.UTC) },
		bytes.NewReader([]byte{250, 0, 1, 2, 3, 4, 5}),
	)
	if err != nil {
		t.Fatalf("new generator: %v", err)
	}

	value, err := generator.Generate()
	if err != nil {
		t.Fatalf("generate id_user: %v", err)
	}
	if value[:6] != "bw0123" {
		t.Fatalf("unexpected random segment: %q", value)
	}
}

func TestIDUserRandomSourceError(t *testing.T) {
	t.Parallel()

	generator, err := New(time.Now, errorReader{})
	if err != nil {
		t.Fatalf("new generator: %v", err)
	}
	if _, err := generator.Generate(); !errors.Is(err, errRandom) {
		t.Fatalf("error = %v", err)
	}
}

var errRandom = errors.New("random unavailable")

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errRandom
}
