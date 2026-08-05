package observability

import (
	"strings"
	"testing"
)

func TestCurrentUserCacheMetricsUseBoundedOperationOutcomePairs(t *testing.T) {
	t.Parallel()

	metrics, err := New("identity-service", nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	valid := [][2]string{
		{CacheOperationGet, CacheOutcomeHit},
		{CacheOperationGet, CacheOutcomeMiss},
		{CacheOperationGet, CacheOutcomeError},
		{CacheOperationGet, CacheOutcomeInvalid},
		{CacheOperationSet, CacheOutcomeSuccess},
		{CacheOperationSet, CacheOutcomeError},
		{CacheOperationDelete, CacheOutcomeSuccess},
		{CacheOperationDelete, CacheOutcomeError},
	}
	for _, pair := range valid {
		metrics.ObserveCurrentUserCache(pair[0], pair[1])
	}
	for _, pair := range [][2]string{
		{"user_provider_123", CacheOutcomeHit},
		{CacheOperationGet, "user_provider_123"},
		{CacheOperationSet, CacheOutcomeHit},
		{CacheOperationDelete, CacheOutcomeInvalid},
	} {
		metrics.ObserveCurrentUserCache(pair[0], pair[1])
	}

	body := scrapeMetrics(t, metrics)
	for _, pair := range valid {
		expected := `current_user_cache_operations_total{operation="` + pair[0] + `",outcome="` + pair[1] + `",service="identity-service"} 1`
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics missing %q\n%s", expected, body)
		}
	}
	for _, forbidden := range []string{
		"user_provider_123",
		"bridgeworks:identity:current-user",
		"cache.example.upstash.io",
		"redis-password",
		"request_id",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("metrics leaked %q: %s", forbidden, body)
		}
	}
}
