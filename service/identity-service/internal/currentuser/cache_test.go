package currentuser

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeCache struct {
	mu          sync.Mutex
	values      map[string][]byte
	getError    error
	setError    error
	deleteError error
	getCalls    int
	setCalls    int
	deleteCalls int
	lastKey     string
	lastTTL     time.Duration
}

func newFakeCache() *fakeCache {
	return &fakeCache{values: make(map[string][]byte)}
}

func (c *fakeCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getCalls++
	c.lastKey = key
	if c.getError != nil {
		return nil, false, c.getError
	}
	value, ok := c.values[key]
	return append([]byte(nil), value...), ok, nil
}

func (c *fakeCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setCalls++
	c.lastKey = key
	c.lastTTL = ttl
	if c.setError != nil {
		return c.setError
	}
	c.values[key] = append([]byte(nil), value...)
	return nil
}

func (c *fakeCache) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleteCalls++
	c.lastKey = key
	if c.deleteError != nil {
		return c.deleteError
	}
	delete(c.values, key)
	return nil
}

func (c *fakeCache) snapshot() (get, set, deleted int, key string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.getCalls, c.setCalls, c.deleteCalls, c.lastKey, c.lastTTL
}

type cacheObserver struct {
	mu     sync.Mutex
	counts map[string]int
}

func newCacheObserver() *cacheObserver {
	return &cacheObserver{counts: make(map[string]int)}
}

func (o *cacheObserver) ObserveCurrentUserCache(operation, outcome string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.counts[operation+":"+outcome]++
}

func (o *cacheObserver) count(operation, outcome string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.counts[operation+":"+outcome]
}

func TestCachedReaderHitAvoidsPostgreSQL(t *testing.T) {
	t.Parallel()

	user := cacheTestUser("active")
	cache := newFakeCache()
	key, _ := CurrentUserCacheKey(user.ClerkUserID)
	payload, err := encodeCacheValue(user)
	if err != nil {
		t.Fatalf("encode cache value: %v", err)
	}
	cache.values[key] = payload
	observer := newCacheObserver()
	var reads atomic.Int32
	reader, err := NewCachedReader(readerFunc(func(context.Context, string) (User, bool, error) {
		reads.Add(1)
		return User{}, false, errors.New("PostgreSQL must not be called")
	}), cache, 30*time.Second, 100*time.Millisecond, observer)
	if err != nil {
		t.Fatalf("NewCachedReader: %v", err)
	}

	got, found, err := reader.GetCurrentUserByClerkUserID(context.Background(), user.ClerkUserID)
	if err != nil || !found {
		t.Fatalf("read cache hit: found=%v err=%v", found, err)
	}
	if reads.Load() != 0 {
		t.Fatalf("PostgreSQL reads = %d", reads.Load())
	}
	if got.ClerkUserID != user.ClerkUserID || got.ID != user.ID || got.IDUser != user.IDUser {
		t.Fatalf("unexpected cached user: %+v", got)
	}
	if observer.count(cacheOperationGet, cacheOutcomeHit) != 1 {
		t.Fatalf("hit metric missing: %+v", observer.counts)
	}
}

func TestCachedReaderMissLoadsPostgreSQLAndPopulatesCache(t *testing.T) {
	t.Parallel()

	user := cacheTestUser("active")
	cache := newFakeCache()
	observer := newCacheObserver()
	var reads atomic.Int32
	reader, err := NewCachedReader(readerFunc(func(_ context.Context, clerkUserID string) (User, bool, error) {
		reads.Add(1)
		if clerkUserID != user.ClerkUserID {
			t.Fatalf("clerkUserID = %q", clerkUserID)
		}
		return user, true, nil
	}), cache, 30*time.Second, 100*time.Millisecond, observer)
	if err != nil {
		t.Fatalf("NewCachedReader: %v", err)
	}

	got, found, err := reader.GetCurrentUserByClerkUserID(context.Background(), user.ClerkUserID)
	if err != nil || !found || got.ID != user.ID {
		t.Fatalf("read miss: got=%+v found=%v err=%v", got, found, err)
	}
	if reads.Load() != 1 {
		t.Fatalf("PostgreSQL reads = %d", reads.Load())
	}
	getCalls, setCalls, _, key, ttl := cache.snapshot()
	if getCalls != 1 || setCalls != 1 || ttl != 30*time.Second {
		t.Fatalf("cache calls get=%d set=%d ttl=%s", getCalls, setCalls, ttl)
	}
	if strings.Contains(key, user.ClerkUserID) {
		t.Fatalf("cache key leaked Clerk user ID: %q", key)
	}
	if observer.count(cacheOperationGet, cacheOutcomeMiss) != 1 ||
		observer.count(cacheOperationSet, cacheOutcomeSuccess) != 1 {
		t.Fatalf("unexpected metrics: %+v", observer.counts)
	}
}

type timeoutCache struct{}

func (timeoutCache) Get(ctx context.Context, _ string) ([]byte, bool, error) {
	<-ctx.Done()
	return nil, false, ctx.Err()
}

func (timeoutCache) Set(context.Context, string, []byte, time.Duration) error {
	return nil
}

func (timeoutCache) Delete(context.Context, string) error {
	return nil
}

func TestCachedReaderGetTimeoutFallsBackToPostgreSQL(t *testing.T) {
	t.Parallel()

	user := cacheTestUser("active")
	observer := newCacheObserver()
	var reads atomic.Int32
	reader, err := NewCachedReader(readerFunc(func(context.Context, string) (User, bool, error) {
		reads.Add(1)
		return user, true, nil
	}), timeoutCache{}, 30*time.Second, 20*time.Millisecond, observer)
	if err != nil {
		t.Fatalf("NewCachedReader: %v", err)
	}

	started := time.Now()
	got, found, err := reader.GetCurrentUserByClerkUserID(context.Background(), user.ClerkUserID)
	elapsed := time.Since(started)
	if err != nil || !found || got.ID != user.ID {
		t.Fatalf("timeout fallback: got=%+v found=%v err=%v", got, found, err)
	}
	if reads.Load() != 1 {
		t.Fatalf("PostgreSQL reads = %d", reads.Load())
	}
	if elapsed < 20*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("timeout fallback elapsed = %s", elapsed)
	}
	if observer.count(cacheOperationGet, cacheOutcomeError) != 1 {
		t.Fatalf("get error metric missing: %+v", observer.counts)
	}
}

func TestCachedReaderCacheErrorsFallBackWithoutChangingPostgreSQLResult(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		getError error
		setError error
	}{
		{name: "GET error", getError: errors.New("redis unavailable")},
		{name: "SET error", setError: errors.New("redis unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			user := cacheTestUser("active")
			cache := newFakeCache()
			cache.getError = test.getError
			cache.setError = test.setError
			observer := newCacheObserver()
			reader, err := NewCachedReader(readerFunc(func(context.Context, string) (User, bool, error) {
				return user, true, nil
			}), cache, 30*time.Second, 100*time.Millisecond, observer)
			if err != nil {
				t.Fatalf("NewCachedReader: %v", err)
			}
			got, found, err := reader.GetCurrentUserByClerkUserID(context.Background(), user.ClerkUserID)
			if err != nil || !found || got.ID != user.ID {
				t.Fatalf("fallback result: got=%+v found=%v err=%v", got, found, err)
			}
			if test.getError != nil && observer.count(cacheOperationGet, cacheOutcomeError) != 1 {
				t.Fatalf("GET error metric missing: %+v", observer.counts)
			}
			if test.setError != nil && observer.count(cacheOperationSet, cacheOutcomeError) != 1 {
				t.Fatalf("SET error metric missing: %+v", observer.counts)
			}
		})
	}
}

func TestCachedReaderCorruptionDeletesAndFallsBack(t *testing.T) {
	t.Parallel()

	for _, payload := range [][]byte{
		[]byte(`{"schema_version":1`),
		[]byte(`{"schema_version":2,"id":"0198f3be-bf6f-7b0a-8a25-f8433567e0c1","id_user":"bw012303082645","primary_email":null,"status":"active","created_at":"2026-08-03T03:04:05Z","updated_at":"2026-08-03T03:04:05Z"}`),
		[]byte(`{"schema_version":1,"id":"0198f3be-bf6f-7b0a-8a25-f8433567e0c1","id_user":"bw012303082645","primary_email":null,"status":"active","created_at":"2026-08-03T03:04:05Z","updated_at":"2026-08-03T03:04:05Z","unexpected":true}`),
	} {
		payload := payload
		t.Run(string(payload[:min(len(payload), 20)]), func(t *testing.T) {
			t.Parallel()
			user := cacheTestUser("active")
			cache := newFakeCache()
			key, _ := CurrentUserCacheKey(user.ClerkUserID)
			cache.values[key] = payload
			observer := newCacheObserver()
			var reads atomic.Int32
			reader, err := NewCachedReader(readerFunc(func(context.Context, string) (User, bool, error) {
				reads.Add(1)
				return user, true, nil
			}), cache, 30*time.Second, 100*time.Millisecond, observer)
			if err != nil {
				t.Fatalf("NewCachedReader: %v", err)
			}
			got, found, err := reader.GetCurrentUserByClerkUserID(context.Background(), user.ClerkUserID)
			if err != nil || !found || got.ID != user.ID {
				t.Fatalf("corrupt fallback: got=%+v found=%v err=%v", got, found, err)
			}
			if reads.Load() != 1 {
				t.Fatalf("PostgreSQL reads = %d", reads.Load())
			}
			_, _, deletes, _, _ := cache.snapshot()
			if deletes != 1 {
				t.Fatalf("cache deletes = %d", deletes)
			}
			if observer.count(cacheOperationGet, cacheOutcomeInvalid) != 1 ||
				observer.count(cacheOperationDelete, cacheOutcomeSuccess) != 1 {
				t.Fatalf("unexpected metrics: %+v", observer.counts)
			}
		})
	}
}

func TestCachedReaderDoesNotNegativeCacheMissingProjection(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	var reads atomic.Int32
	reader, err := NewCachedReader(readerFunc(func(context.Context, string) (User, bool, error) {
		reads.Add(1)
		return User{}, false, nil
	}), cache, 30*time.Second, 100*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("NewCachedReader: %v", err)
	}
	for range 2 {
		_, found, err := reader.GetCurrentUserByClerkUserID(context.Background(), "user_missing")
		if err != nil || found {
			t.Fatalf("missing result found=%v err=%v", found, err)
		}
	}
	if reads.Load() != 2 {
		t.Fatalf("PostgreSQL reads = %d, want 2", reads.Load())
	}
	_, sets, _, _, _ := cache.snapshot()
	if sets != 0 {
		t.Fatalf("negative cache SET calls = %d", sets)
	}
}

func TestCachedReaderPreservesLifecyclePolicyAndPostgreSQLErrors(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		status    string
		wantError error
	}{
		{status: "active"},
		{status: "disabled", wantError: ErrAccountDisabled},
		{status: "deleted", wantError: ErrAccountDeleted},
	} {
		t.Run(test.status, func(t *testing.T) {
			t.Parallel()
			user := cacheTestUser(test.status)
			cache := newFakeCache()
			key, _ := CurrentUserCacheKey(user.ClerkUserID)
			cache.values[key], _ = encodeCacheValue(user)
			cached, _ := NewCachedReader(readerFunc(func(context.Context, string) (User, bool, error) {
				return User{}, false, errors.New("must not read PostgreSQL")
			}), cache, 30*time.Second, 100*time.Millisecond, nil)
			service := New(cached)
			got, err := service.Get(context.Background(), user.ClerkUserID)
			if test.wantError != nil {
				if !errors.Is(err, test.wantError) {
					t.Fatalf("error = %v, want %v", err, test.wantError)
				}
				return
			}
			if err != nil || got.Status != "active" {
				t.Fatalf("active result: got=%+v err=%v", got, err)
			}
		})
	}

	postgresError := errors.New("database unavailable")
	cached, _ := NewCachedReader(readerFunc(func(context.Context, string) (User, bool, error) {
		return User{}, false, postgresError
	}), newFakeCache(), 30*time.Second, 100*time.Millisecond, nil)
	_, _, err := cached.GetCurrentUserByClerkUserID(context.Background(), "user_error")
	if !errors.Is(err, postgresError) {
		t.Fatalf("PostgreSQL error = %v", err)
	}
}

func TestCacheValuePreservesUTCAndExcludesClerkUserID(t *testing.T) {
	t.Parallel()

	user := cacheTestUser("active")
	user.CreatedAt = time.Date(2026, time.August, 3, 10, 4, 5, 123456789, time.FixedZone("ICT", 7*60*60))
	user.UpdatedAt = user.CreatedAt.Add(time.Second)
	payload, err := encodeCacheValue(user)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(string(payload), user.ClerkUserID) {
		t.Fatalf("cache value leaked Clerk user ID: %s", payload)
	}
	decoded, err := decodeCacheValue(payload, user.ClerkUserID)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.CreatedAt.Location() != time.UTC || decoded.UpdatedAt.Location() != time.UTC {
		t.Fatalf("timestamps are not UTC: created=%s updated=%s", decoded.CreatedAt.Location(), decoded.UpdatedAt.Location())
	}
	if !decoded.CreatedAt.Equal(user.CreatedAt) || !decoded.UpdatedAt.Equal(user.UpdatedAt) {
		t.Fatalf("timestamp precision changed: got=%s/%s want=%s/%s", decoded.CreatedAt, decoded.UpdatedAt, user.CreatedAt, user.UpdatedAt)
	}
}

func TestCurrentUserCacheKeyIsDeterministicBoundedAndPrivate(t *testing.T) {
	t.Parallel()

	raw := "user_provider_sensitive_123"
	first, err := CurrentUserCacheKey(raw)
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}
	second, _ := CurrentUserCacheKey(raw)
	if first != second {
		t.Fatalf("keys differ: %q %q", first, second)
	}
	if strings.Contains(first, raw) || !strings.HasPrefix(first, cacheKeyPrefix) || len(first) != len(cacheKeyPrefix)+64 {
		t.Fatalf("invalid private key: %q", first)
	}
	if _, err := CurrentUserCacheKey("   "); err == nil {
		t.Fatal("blank identity was accepted")
	}
}

func TestCachedReaderCoalescesSameKeyMisses(t *testing.T) {
	t.Parallel()

	user := cacheTestUser("active")
	cache := newFakeCache()
	started := make(chan struct{})
	release := make(chan struct{})
	var reads atomic.Int32
	reader, _ := NewCachedReader(readerFunc(func(context.Context, string) (User, bool, error) {
		if reads.Add(1) == 1 {
			close(started)
		}
		<-release
		return user, true, nil
	}), cache, 30*time.Second, 100*time.Millisecond, nil)

	const callers = 24
	start := make(chan struct{})
	errorsChannel := make(chan error, callers)
	for range callers {
		go func() {
			<-start
			got, found, err := reader.GetCurrentUserByClerkUserID(context.Background(), user.ClerkUserID)
			if err != nil || !found || got.ID != user.ID {
				errorsChannel <- errors.New("unexpected coalesced result")
				return
			}
			errorsChannel <- nil
		}()
	}
	close(start)
	<-started
	time.Sleep(20 * time.Millisecond)
	close(release)
	for range callers {
		if err := <-errorsChannel; err != nil {
			t.Fatal(err)
		}
	}
	if reads.Load() != 1 {
		t.Fatalf("PostgreSQL reads = %d, want 1", reads.Load())
	}
}

func TestCanceledCallerDoesNotCancelSharedLoad(t *testing.T) {
	t.Parallel()

	user := cacheTestUser("active")
	started := make(chan struct{})
	release := make(chan struct{})
	var reads atomic.Int32
	reader, _ := NewCachedReader(readerFunc(func(ctx context.Context, _ string) (User, bool, error) {
		if reads.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return user, true, nil
		case <-ctx.Done():
			return User{}, false, ctx.Err()
		}
	}), newFakeCache(), 30*time.Second, 100*time.Millisecond, nil)

	firstContext, cancelFirst := context.WithCancel(context.Background())
	firstResult := make(chan error, 1)
	go func() {
		_, _, err := reader.GetCurrentUserByClerkUserID(firstContext, user.ClerkUserID)
		firstResult <- err
	}()
	<-started
	cancelFirst()
	if err := <-firstResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("first caller error = %v", err)
	}

	secondResult := make(chan error, 1)
	go func() {
		got, found, err := reader.GetCurrentUserByClerkUserID(context.Background(), user.ClerkUserID)
		if err == nil && (!found || got.ID != user.ID) {
			err = errors.New("unexpected second result")
		}
		secondResult <- err
	}()
	close(release)
	if err := <-secondResult; err != nil {
		t.Fatalf("second caller error = %v", err)
	}
	if reads.Load() != 1 {
		t.Fatalf("PostgreSQL reads = %d, want 1", reads.Load())
	}
}

func TestCachedReaderDoesNotSerializeDifferentKeys(t *testing.T) {
	t.Parallel()

	started := make(chan string, 2)
	release := make(chan struct{})
	reader, _ := NewCachedReader(readerFunc(func(_ context.Context, clerkUserID string) (User, bool, error) {
		started <- clerkUserID
		<-release
		user := cacheTestUser("active")
		user.ClerkUserID = clerkUserID
		return user, true, nil
	}), newFakeCache(), 30*time.Second, 100*time.Millisecond, nil)

	results := make(chan error, 2)
	for _, id := range []string{"user_a", "user_b"} {
		id := id
		go func() {
			_, _, err := reader.GetCurrentUserByClerkUserID(context.Background(), id)
			results <- err
		}()
	}
	seen := map[string]bool{}
	for range 2 {
		select {
		case id := <-started:
			seen[id] = true
		case <-time.After(time.Second):
			t.Fatal("different-key load was globally serialized")
		}
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("different-key result: %v", err)
		}
	}
	if !seen["user_a"] || !seen["user_b"] {
		t.Fatalf("started keys = %+v", seen)
	}
}

func TestCacheInvalidatorUsesPrivateKeyAndBoundedMetrics(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	observer := newCacheObserver()
	invalidator, err := NewCacheInvalidator(cache, 100*time.Millisecond, observer)
	if err != nil {
		t.Fatalf("NewCacheInvalidator: %v", err)
	}
	if err := invalidator.Invalidate(context.Background(), "user_secret"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	_, _, deletes, key, _ := cache.snapshot()
	if deletes != 1 || strings.Contains(key, "user_secret") {
		t.Fatalf("delete calls=%d key=%q", deletes, key)
	}
	if observer.count(cacheOperationDelete, cacheOutcomeSuccess) != 1 {
		t.Fatalf("delete metric missing: %+v", observer.counts)
	}

	cache.deleteError = errors.New("redis unavailable")
	if err := invalidator.Invalidate(context.Background(), "user_secret"); err == nil {
		t.Fatal("delete error was suppressed")
	}
	if observer.count(cacheOperationDelete, cacheOutcomeError) != 1 {
		t.Fatalf("delete error metric missing: %+v", observer.counts)
	}
}

func cacheTestUser(status string) User {
	email := "developer@example.com"
	return User{
		ID:           uuid.MustParse("0198f3be-bf6f-7b0a-8a25-f8433567e0c1"),
		ClerkUserID:  "user_cache_test",
		PrimaryEmail: &email,
		IDUser:       "bw012303082645",
		Status:       status,
		CreatedAt:    time.Date(2026, time.August, 3, 3, 4, 5, 123456789, time.UTC),
		UpdatedAt:    time.Date(2026, time.August, 3, 3, 5, 6, 987654321, time.UTC),
	}
}
