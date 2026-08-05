package rediscache

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
)

const defaultPoolSize = 10

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

func (c *Client) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}
