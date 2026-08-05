package currentuser

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

const (
	cacheSchemaVersion   = 1
	cacheKeyPrefix       = "bridgeworks:identity:current-user:v1:"
	maxCacheValueBytes   = 4 * 1024
	coalescedLoadTimeout = 2 * time.Second
	cacheOperationGet    = "get"
	cacheOperationSet    = "set"
	cacheOperationDelete = "delete"
	cacheOutcomeHit      = "hit"
	cacheOutcomeMiss     = "miss"
	cacheOutcomeSuccess  = "success"
	cacheOutcomeError    = "error"
	cacheOutcomeInvalid  = "invalid"
)

var idUserPattern = regexp.MustCompile(`^bw[0-9]{12}$`)

type Cache interface {
	Get(context.Context, string) ([]byte, bool, error)
	Set(context.Context, string, []byte, time.Duration) error
	Delete(context.Context, string) error
}

type CacheObserver interface {
	ObserveCurrentUserCache(operation, outcome string)
}

type CachedReader struct {
	reader           Reader
	cache            Cache
	ttl              time.Duration
	operationTimeout time.Duration
	observer         CacheObserver
	loads            singleflight.Group
}

type CacheInvalidator struct {
	cache            Cache
	operationTimeout time.Duration
	observer         CacheObserver
}

type cacheEnvelope struct {
	SchemaVersion int     `json:"schema_version"`
	ID            string  `json:"id"`
	IDUser        string  `json:"id_user"`
	PrimaryEmail  *string `json:"primary_email"`
	Status        string  `json:"status"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

type readResult struct {
	user  User
	found bool
}

func NewCachedReader(
	reader Reader,
	cache Cache,
	ttl time.Duration,
	operationTimeout time.Duration,
	observer CacheObserver,
) (*CachedReader, error) {
	if reader == nil {
		return nil, errors.New("current user PostgreSQL reader is required")
	}
	if cache == nil {
		return nil, errors.New("current user cache is required")
	}
	if ttl <= 0 {
		return nil, errors.New("current user cache TTL must be greater than zero")
	}
	if operationTimeout <= 0 {
		return nil, errors.New("current user cache operation timeout must be greater than zero")
	}
	return &CachedReader{
		reader:           reader,
		cache:            cache,
		ttl:              ttl,
		operationTimeout: operationTimeout,
		observer:         observer,
	}, nil
}

func NewCacheInvalidator(
	cache Cache,
	operationTimeout time.Duration,
	observer CacheObserver,
) (*CacheInvalidator, error) {
	if cache == nil {
		return nil, errors.New("current user cache is required")
	}
	if operationTimeout <= 0 {
		return nil, errors.New("current user cache operation timeout must be greater than zero")
	}
	return &CacheInvalidator{
		cache:            cache,
		operationTimeout: operationTimeout,
		observer:         observer,
	}, nil
}

func CurrentUserCacheKey(clerkUserID string) (string, error) {
	if strings.TrimSpace(clerkUserID) == "" {
		return "", errors.New("current user cache identity is required")
	}
	digest := sha256.Sum256([]byte(clerkUserID))
	return cacheKeyPrefix + hex.EncodeToString(digest[:]), nil
}

func (r *CachedReader) GetCurrentUserByClerkUserID(
	ctx context.Context,
	clerkUserID string,
) (User, bool, error) {
	if r == nil || r.reader == nil || r.cache == nil {
		return User{}, false, errors.New("current user cached reader is not initialized")
	}
	key, err := CurrentUserCacheKey(clerkUserID)
	if err != nil {
		return User{}, false, err
	}

	if user, hit := r.get(ctx, key, clerkUserID); hit {
		return user, true, nil
	}

	resultChannel := r.loads.DoChan(key, func() (any, error) {
		loadContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), coalescedLoadTimeout)
		defer cancel()

		user, found, loadErr := r.reader.GetCurrentUserByClerkUserID(loadContext, clerkUserID)
		if loadErr != nil {
			return readResult{}, loadErr
		}
		if found {
			r.set(loadContext, key, user)
		}
		return readResult{user: user, found: found}, nil
	})

	select {
	case <-ctx.Done():
		return User{}, false, ctx.Err()
	case result := <-resultChannel:
		if result.Err != nil {
			return User{}, false, result.Err
		}
		value, ok := result.Val.(readResult)
		if !ok {
			return User{}, false, errors.New("current user coalesced load returned an invalid result")
		}
		return value.user, value.found, nil
	}
}

func (r *CachedReader) get(ctx context.Context, key, clerkUserID string) (User, bool) {
	operationContext, cancel := context.WithTimeout(ctx, r.operationTimeout)
	defer cancel()

	payload, found, err := r.cache.Get(operationContext, key)
	if err != nil {
		r.observe(cacheOperationGet, cacheOutcomeError)
		return User{}, false
	}
	if !found {
		r.observe(cacheOperationGet, cacheOutcomeMiss)
		return User{}, false
	}

	user, err := decodeCacheValue(payload, clerkUserID)
	if err != nil {
		r.observe(cacheOperationGet, cacheOutcomeInvalid)
		r.delete(ctx, key)
		return User{}, false
	}
	r.observe(cacheOperationGet, cacheOutcomeHit)
	return user, true
}

func (r *CachedReader) set(ctx context.Context, key string, user User) {
	payload, err := encodeCacheValue(user)
	if err != nil {
		r.observe(cacheOperationSet, cacheOutcomeError)
		return
	}
	operationContext, cancel := context.WithTimeout(ctx, r.operationTimeout)
	defer cancel()
	if err := r.cache.Set(operationContext, key, payload, r.ttl); err != nil {
		r.observe(cacheOperationSet, cacheOutcomeError)
		return
	}
	r.observe(cacheOperationSet, cacheOutcomeSuccess)
}

func (r *CachedReader) delete(ctx context.Context, key string) {
	operationContext, cancel := context.WithTimeout(ctx, r.operationTimeout)
	defer cancel()
	if err := r.cache.Delete(operationContext, key); err != nil {
		r.observe(cacheOperationDelete, cacheOutcomeError)
		return
	}
	r.observe(cacheOperationDelete, cacheOutcomeSuccess)
}

func (r *CachedReader) observe(operation, outcome string) {
	if r != nil && r.observer != nil {
		r.observer.ObserveCurrentUserCache(operation, outcome)
	}
}

func (i *CacheInvalidator) Invalidate(ctx context.Context, clerkUserID string) error {
	if i == nil || i.cache == nil {
		return errors.New("current user cache invalidator is not initialized")
	}
	key, err := CurrentUserCacheKey(clerkUserID)
	if err != nil {
		return err
	}
	operationContext, cancel := context.WithTimeout(ctx, i.operationTimeout)
	defer cancel()
	if err := i.cache.Delete(operationContext, key); err != nil {
		i.observe(cacheOperationDelete, cacheOutcomeError)
		return err
	}
	i.observe(cacheOperationDelete, cacheOutcomeSuccess)
	return nil
}

func (i *CacheInvalidator) observe(operation, outcome string) {
	if i != nil && i.observer != nil {
		i.observer.ObserveCurrentUserCache(operation, outcome)
	}
}

func encodeCacheValue(user User) ([]byte, error) {
	if err := validateCacheUser(user); err != nil {
		return nil, err
	}
	envelope := cacheEnvelope{
		SchemaVersion: cacheSchemaVersion,
		ID:            user.ID.String(),
		IDUser:        user.IDUser,
		PrimaryEmail:  cloneStringPointer(user.PrimaryEmail),
		Status:        user.Status,
		CreatedAt:     user.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:     user.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	return json.Marshal(envelope)
}

func decodeCacheValue(payload []byte, clerkUserID string) (User, error) {
	if len(payload) == 0 || len(payload) > maxCacheValueBytes {
		return User{}, errors.New("current user cache value has an invalid size")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var envelope cacheEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return User{}, errors.New("current user cache value is malformed")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return User{}, errors.New("current user cache value has trailing data")
	}
	if envelope.SchemaVersion != cacheSchemaVersion {
		return User{}, errors.New("current user cache value has an unsupported schema version")
	}

	id, err := uuid.Parse(envelope.ID)
	if err != nil || id == uuid.Nil || id.Version() != 7 {
		return User{}, errors.New("current user cache value has an invalid user ID")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, envelope.CreatedAt)
	if err != nil || createdAt.IsZero() || !isUTCOffset(createdAt) {
		return User{}, errors.New("current user cache value has an invalid created timestamp")
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, envelope.UpdatedAt)
	if err != nil || updatedAt.IsZero() || !isUTCOffset(updatedAt) || updatedAt.Before(createdAt) {
		return User{}, errors.New("current user cache value has an invalid updated timestamp")
	}

	user := User{
		ID:           id,
		ClerkUserID:  clerkUserID,
		PrimaryEmail: cloneStringPointer(envelope.PrimaryEmail),
		IDUser:       envelope.IDUser,
		Status:       envelope.Status,
		CreatedAt:    createdAt.UTC(),
		UpdatedAt:    updatedAt.UTC(),
	}
	if err := validateCacheUser(user); err != nil {
		return User{}, err
	}
	return user, nil
}

func validateCacheUser(user User) error {
	if user.ID == uuid.Nil || user.ID.Version() != 7 {
		return errors.New("current user cache value has an invalid user ID")
	}
	if !idUserPattern.MatchString(user.IDUser) {
		return errors.New("current user cache value has an invalid public ID")
	}
	switch user.Status {
	case "active", "disabled", "deleted":
	default:
		return errors.New("current user cache value has an invalid lifecycle status")
	}
	if user.PrimaryEmail != nil {
		value := *user.PrimaryEmail
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return errors.New("current user cache value has an invalid primary email")
		}
	}
	if user.CreatedAt.IsZero() || user.UpdatedAt.IsZero() || user.UpdatedAt.Before(user.CreatedAt) {
		return errors.New("current user cache value has invalid timestamps")
	}
	return nil
}

func isUTCOffset(value time.Time) bool {
	_, offset := value.Zone()
	return offset == 0
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
