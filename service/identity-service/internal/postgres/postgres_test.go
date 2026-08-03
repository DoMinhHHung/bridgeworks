package postgres

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOpenRedactsDatabaseURLFromParseError(t *testing.T) {
	t.Parallel()

	const secretURL = "postgres://runtime:super-secret@identity-postgres:5432/%zz"

	_, err := Open(context.Background(), Config{
		URL:               secretURL,
		ConnectTimeout:    time.Second,
		MaxConns:          1,
		MinConns:          0,
		MaxConnLifetime:   time.Minute,
		MaxConnIdleTime:   time.Minute,
		HealthCheckPeriod: time.Minute,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), secretURL) ||
		strings.Contains(err.Error(), "super-secret") ||
		strings.Contains(err.Error(), "identity-postgres") {
		t.Fatalf("database connection details leaked: %q", err)
	}
	if err.Error() != "parse postgres pool configuration" {
		t.Fatalf("unexpected error: %q", err)
	}
}

func TestNilDBOperationsAreSafe(t *testing.T) {
	t.Parallel()

	var db *DB
	if err := db.Ping(context.Background()); err == nil {
		t.Fatal("expected ping error")
	}
	db.Close()
}
