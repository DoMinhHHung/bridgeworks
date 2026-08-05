package rediscache

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	redis "github.com/redis/go-redis/v9"
)

const defaultPoolSize = 10

var configureLibraryLoggerOnce sync.Once

var setIfGenerationScript = redis.NewScript(`
local current = redis.call("GET", KEYS[2])
if not current then
  current = "0"
end
if current ~= ARGV[1] then
  return 0
end
redis.call("SET", KEYS[1], ARGV[2], "PX", ARGV[3])
return 1
`)

var invalidateScript = redis.NewScript(`
redis.call("INCR", KEYS[2])
redis.call("PEXPIRE", KEYS[2], ARGV[1])
redis.call("DEL", KEYS[1])
return 1
`)

type voidLogger struct{}

func (voidLogger) Printf(context.Context, string, ...interface{}) {}

type Config struct {
	Addr             string
	Username         string
	Password         string
	TLSEnabled       bool
	DialTimeout      time.Duration
	OperationTimeout time.Duration
}

type Client struct {
	client *redis.Client
}

func New(cfg Config) (*Client, error) {
	host, _, err := net.SplitHostPort(strings.TrimSpace(cfg.Addr))
	if err != nil || strings.TrimSpace(host) == "" {
		return nil, errors.New("redis address must be a valid host and port")
	}
	if strings.TrimSpace(cfg.Username) == "" {
		return nil, errors.New("redis username must not be empty")
	}
	if strings.TrimSpace(cfg.Password) == "" {
		return nil, errors.New("redis password is required")
	}
	if cfg.DialTimeout <= 0 {
		return nil, errors.New("redis dial timeout must be greater than zero")
	}
	if cfg.OperationTimeout <= 0 {
		return nil, errors.New("redis operation timeout must be greater than zero")
	}

	// go-redis writes connection-pool diagnostics, including raw network errors,
	// to stderr through a package-global logger. Identity owns bounded cache
	// telemetry and sanitized application warnings, so suppress the dependency
	// logger before constructing the first client.
	configureLibraryLoggerOnce.Do(func() {
		redis.SetLogger(voidLogger{})
	})

	options := &redis.Options{
		Addr:                  strings.TrimSpace(cfg.Addr),
		Username:              strings.TrimSpace(cfg.Username),
		Password:              cfg.Password,
		DialTimeout:           cfg.DialTimeout,
		ReadTimeout:           cfg.OperationTimeout,
		WriteTimeout:          cfg.OperationTimeout,
		PoolTimeout:           cfg.OperationTimeout,
		PoolSize:              defaultPoolSize,
		MinIdleConns:          0,
		MaxRetries:            -1,
		ContextTimeoutEnabled: true,
	}
	if cfg.TLSEnabled {
		options.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: host,
		}
	}
	return &Client{client: redis.NewClient(options)}, nil
}

func (c *Client) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if c == nil || c.client == nil {
		return nil, false, errors.New("redis cache client is not initialized")
	}
	value, err := c.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func (c *Client) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if c == nil || c.client == nil {
		return errors.New("redis cache client is not initialized")
	}
	return c.client.Set(ctx, key, value, ttl).Err()
}

func (c *Client) Delete(ctx context.Context, key string) error {
	if c == nil || c.client == nil {
		return errors.New("redis cache client is not initialized")
	}
	return c.client.Del(ctx, key).Err()
}

func (c *Client) GetGeneration(ctx context.Context, key string) (uint64, error) {
	if c == nil || c.client == nil {
		return 0, errors.New("redis cache client is not initialized")
	}
	value, err := c.client.Get(ctx, key).Uint64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return value, nil
}

func (c *Client) SetIfGeneration(
	ctx context.Context,
	key string,
	generationKey string,
	expectedGeneration uint64,
	value []byte,
	ttl time.Duration,
) (bool, error) {
	if c == nil || c.client == nil {
		return false, errors.New("redis cache client is not initialized")
	}
	ttlMilliseconds := ttl.Milliseconds()
	if ttlMilliseconds <= 0 {
		return false, errors.New("redis cache TTL must be at least one millisecond")
	}
	result, err := setIfGenerationScript.Run(
		ctx,
		c.client,
		[]string{key, generationKey},
		strconv.FormatUint(expectedGeneration, 10),
		value,
		strconv.FormatInt(ttlMilliseconds, 10),
	).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func (c *Client) Invalidate(
	ctx context.Context,
	key string,
	generationKey string,
	generationTTL time.Duration,
) error {
	if c == nil || c.client == nil {
		return errors.New("redis cache client is not initialized")
	}
	generationTTLMilliseconds := generationTTL.Milliseconds()
	if generationTTLMilliseconds <= 0 {
		return errors.New("redis cache generation TTL must be at least one millisecond")
	}
	return invalidateScript.Run(
		ctx,
		c.client,
		[]string{key, generationKey},
		strconv.FormatInt(generationTTLMilliseconds, 10),
	).Err()
}

func (c *Client) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}
