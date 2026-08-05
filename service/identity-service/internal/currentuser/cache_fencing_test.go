package currentuser

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeFenceState struct {
	generations       map[string]uint64
	generationError  error
}

var fakeFenceStates sync.Map

func fenceState(cache *fakeCache) *fakeFenceState {
	state, _ := fakeFenceStates.LoadOrStore(cache, &fakeFenceState{
		generations: make(map[string]uint64),
	})
	return state.(*fakeFenceState)
}

func (c *fakeCache) GetGeneration(ctx context.Context, key string) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	state := fenceState(c)
	if state.generationError != nil {
		return 0, state.generationError
	}
	return state.generations[key], nil
}

func (c *fakeCache) SetIfGeneration(
	ctx context.Context,
	key string,
	generationKey string,
	expectedGeneration uint64,
	value []byte,
	ttl time.Duration,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setCalls++
	c.lastKey = key
	c.lastTTL = ttl
	if c.setError != nil {
		return false, c.setError
	}
	state := fenceState(c)
	if state.generations[generationKey] != expectedGeneration {
		return false, nil
	}
	c.values[key] = append([]byte(nil), value...)
	return true, nil
}

func (c *fakeCache) Invalidate(
	ctx context.Context,
	key string,
	generationKey string,
	_ time.Duration,
) error {
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
	state := fenceState(c)
	state.generations[generationKey]++
	delete(c.values, key)
	return nil
}

func (timeoutCache) GetGeneration(ctx context.Context, _ string) (uint64, error) {
	<-ctx.Done()
	return 0, ctx.Err()
}

func (timeoutCache) SetIfGeneration(
	context.Context,
	string,
	string,
	uint64,
	[]byte,
	time.Duration,
) (bool, error) {
	return true, nil
}

func (timeoutCache) Invalidate(context.Context, string, string, time.Duration) error {
	return nil
}

func TestCachedReaderRejectsCacheWithoutGenerationFencing(t *testing.T) {
	t.Parallel()

	_, err := NewCachedReader(
		readerFunc(func(context.Context, string) (User, bool, error) {
			return cacheTestUser("active"), true, nil
		}),
		unfencedCache{},
		30*time.Second,
		100*time.Millisecond,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "generation fencing") {
		t.Fatalf("NewCachedReader error = %v", err)
	}
}

type unfencedCache struct{}

func (unfencedCache) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, nil
}
func (unfencedCache) Set(context.Context, string, []byte, time.Duration) error {
	return nil
}
func (unfencedCache) Delete(context.Context, string) error {
	return nil
}

func TestCachedReaderDoesNotWritePreInvalidationSnapshot(t *testing.T) {
	t.Parallel()

	oldUser := cacheTestUser("active")
	newUser := cacheTestUser("deleted")
	newUser.UpdatedAt = oldUser.UpdatedAt.Add(time.Second)

	cache := newFakeCache()
	observer := newCacheObserver()
	readStarted := make(chan struct{})
	releaseRead := make(chan struct{})
	var calls int
	var callsMu sync.Mutex

	reader, err := NewCachedReader(readerFunc(func(context.Context, string) (User, bool, error) {
		callsMu.Lock()
		calls++
		call := calls
		callsMu.Unlock()
		if call == 1 {
			close(readStarted)
			<-releaseRead
			return oldUser, true, nil
		}
		return newUser, true, nil
	}), cache, 30*time.Second, 100*time.Millisecond, observer)
	if err != nil {
		t.Fatalf("NewCachedReader: %v", err)
	}
	invalidator, err := NewCacheInvalidator(cache, 100*time.Millisecond, observer)
	if err != nil {
		t.Fatalf("NewCacheInvalidator: %v", err)
	}

	firstResult := make(chan error, 1)
	go func() {
		got, found, readErr := reader.GetCurrentUserByClerkUserID(context.Background(), oldUser.ClerkUserID)
		if readErr == nil && (!found || got.Status != "active") {
			readErr = errors.New("unexpected first read result")
		}
		firstResult <- readErr
	}()

	<-readStarted
	if err := invalidator.Invalidate(context.Background(), oldUser.ClerkUserID); err != nil {
		t.Fatalf("invalidate while load is in flight: %v", err)
	}
	close(releaseRead)
	if err := <-firstResult; err != nil {
		t.Fatalf("first read: %v", err)
	}

	key, _ := CurrentUserCacheKey(oldUser.ClerkUserID)
	cache.mu.Lock()
	_, staleStored := cache.values[key]
	cache.mu.Unlock()
	if staleStored {
		t.Fatal("pre-invalidation projection was written after invalidation")
	}
	if observer.count(cacheOperationSet, cacheOutcomeStale) != 1 {
		t.Fatalf("stale SET metric missing: %+v", observer.counts)
	}

	got, found, err := reader.GetCurrentUserByClerkUserID(context.Background(), oldUser.ClerkUserID)
	if err != nil || !found || got.Status != "deleted" {
		t.Fatalf("second read: got=%+v found=%v err=%v", got, found, err)
	}
	payload, cached, err := cache.Get(context.Background(), key)
	if err != nil || !cached {
		t.Fatalf("new projection was not cached: cached=%v err=%v", cached, err)
	}
	decoded, err := decodeCacheValue(payload, oldUser.ClerkUserID)
	if err != nil || decoded.Status != "deleted" {
		t.Fatalf("cached projection = %+v err=%v", decoded, err)
	}
}

func TestCacheInvalidationAfterSuccessfulFillDeletesEntryAndAdvancesGeneration(t *testing.T) {
	t.Parallel()

	user := cacheTestUser("active")
	cache := newFakeCache()
	reader, err := NewCachedReader(readerFunc(func(context.Context, string) (User, bool, error) {
		return user, true, nil
	}), cache, 30*time.Second, 100*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("NewCachedReader: %v", err)
	}
	invalidator, err := NewCacheInvalidator(cache, 100*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("NewCacheInvalidator: %v", err)
	}

	if _, found, err := reader.GetCurrentUserByClerkUserID(context.Background(), user.ClerkUserID); err != nil || !found {
		t.Fatalf("fill cache: found=%v err=%v", found, err)
	}
	key, _ := CurrentUserCacheKey(user.ClerkUserID)
	generationKey, _ := CurrentUserCacheGenerationKey(user.ClerkUserID)
	if err := invalidator.Invalidate(context.Background(), user.ClerkUserID); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	cache.mu.Lock()
	_, exists := cache.values[key]
	generation := fenceState(cache).generations[generationKey]
	cache.mu.Unlock()
	if exists {
		t.Fatal("cache entry still exists after invalidation")
	}
	if generation != 1 {
		t.Fatalf("generation = %d, want 1", generation)
	}
}

func TestGenerationReadErrorReturnsPostgreSQLResultWithoutCacheFill(t *testing.T) {
	t.Parallel()

	user := cacheTestUser("active")
	cache := newFakeCache()
	fenceState(cache).generationError = errors.New("redis unavailable")
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
	_, setCalls, _, _, _ := cache.snapshot()
	if setCalls != 0 {
		t.Fatalf("cache SET calls = %d, want 0", setCalls)
	}
	if observer.count(cacheOperationSet, cacheOutcomeError) != 1 {
		t.Fatalf("generation error metric missing: %+v", observer.counts)
	}
}

func TestCurrentUserGenerationKeyIsDeterministicBoundedAndPrivate(t *testing.T) {
	t.Parallel()

	raw := "user_provider_sensitive_123"
	first, err := CurrentUserCacheGenerationKey(raw)
	if err != nil {
		t.Fatalf("derive generation key: %v", err)
	}
	second, _ := CurrentUserCacheGenerationKey(raw)
	if first != second {
		t.Fatalf("generation keys differ: %q %q", first, second)
	}
	if strings.Contains(first, raw) || !strings.HasPrefix(first, cacheGenerationKeyPrefix) || len(first) != len(cacheGenerationKeyPrefix)+64 {
		t.Fatalf("invalid private generation key: %q", first)
	}
}
