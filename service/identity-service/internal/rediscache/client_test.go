package rediscache

import (
	"crypto/tls"
	"strings"
	"testing"
	"time"
)

func TestNewConfiguresBoundedUpstashCompatibleClientWithoutDialing(t *testing.T) {
	t.Parallel()

	client, err := New(Config{
		Addr:             "cache.example.upstash.io:6379",
		Username:         "default",
		Password:         "secret-value",
		TLSEnabled:       true,
		DialTimeout:      500 * time.Millisecond,
		OperationTimeout: 150 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = client.Close() }()

	options := client.client.Options()
	if options.Addr != "cache.example.upstash.io:6379" || options.Username != "default" {
		t.Fatalf("unexpected Redis options: addr=%q username=%q", options.Addr, options.Username)
	}
	if options.DialTimeout != 500*time.Millisecond || options.ReadTimeout != 150*time.Millisecond ||
		options.WriteTimeout != 150*time.Millisecond || options.PoolTimeout != 150*time.Millisecond {
		t.Fatalf("unexpected Redis timeouts: %+v", options)
	}
	if options.PoolSize != defaultPoolSize || options.MinIdleConns != 0 {
		t.Fatalf("unexpected Redis pool options: %+v", options)
	}
	if !options.ContextTimeoutEnabled {
		t.Fatal("Redis client must honor caller context deadlines")
	}
	if options.TLSConfig == nil || options.TLSConfig.MinVersion != tls.VersionTLS12 ||
		options.TLSConfig.ServerName != "cache.example.upstash.io" {
		t.Fatalf("unexpected TLS config: %+v", options.TLSConfig)
	}
}

func TestNewAllowsExplicitLocalPlaintextWithoutNetworkDial(t *testing.T) {
	t.Parallel()

	client, err := New(Config{
		Addr:             "identity-redis:6379",
		Username:         "default",
		Password:         "local-placeholder",
		TLSEnabled:       false,
		DialTimeout:      500 * time.Millisecond,
		OperationTimeout: 150 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = client.Close() }()
	if client.client.Options().TLSConfig != nil {
		t.Fatal("local plaintext client unexpectedly enabled TLS")
	}
}

func TestNewRejectsInvalidConfigWithoutLeakingSecrets(t *testing.T) {
	t.Parallel()

	secret := "redis-password-sensitive"
	address := "rediss://default:" + secret + "@cache.example:6379"
	tests := []Config{
		{Addr: address, Username: "default", Password: secret, DialTimeout: time.Second, OperationTimeout: time.Second},
		{Addr: "cache.example:6379", Username: "", Password: secret, DialTimeout: time.Second, OperationTimeout: time.Second},
		{Addr: "cache.example:6379", Username: "default", Password: "", DialTimeout: time.Second, OperationTimeout: time.Second},
		{Addr: "cache.example:6379", Username: "default", Password: secret, DialTimeout: 0, OperationTimeout: time.Second},
		{Addr: "cache.example:6379", Username: "default", Password: secret, DialTimeout: time.Second, OperationTimeout: 0},
	}
	for _, cfg := range tests {
		_, err := New(cfg)
		if err == nil {
			t.Fatalf("invalid config accepted: %+v", cfg)
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), address) {
			t.Fatalf("error leaked Redis configuration: %q", err)
		}
	}
}
