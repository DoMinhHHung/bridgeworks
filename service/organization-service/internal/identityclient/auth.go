package identityclient

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"cloud.google.com/go/auth"
	googleidtoken "cloud.google.com/go/auth/credentials/idtoken"
)

type platformTokenProvider interface {
	Token(context.Context) (string, error)
}

type clientOptions struct {
	platformTokenProvider platformTokenProvider
}

// Option configures the private Identity Service client.
type Option func(*clientOptions) error

// WithGoogleIDTokenAudience enables Google-authenticated Cloud Run upstream
// requests. Application Default Credentials supply the caller identity.
func WithGoogleIDTokenAudience(audience string) Option {
	return func(options *clientOptions) error {
		if options == nil {
			return errors.New("identity client options must not be nil")
		}
		if options.platformTokenProvider != nil {
			return errors.New("identity platform token provider is already configured")
		}

		provider, err := newGoogleIDTokenProvider(audience)
		if err != nil {
			return err
		}

		options.platformTokenProvider = provider
		return nil
	}
}

func withPlatformTokenProvider(provider platformTokenProvider) Option {
	return func(options *clientOptions) error {
		if options == nil {
			return errors.New("identity client options must not be nil")
		}
		if provider == nil {
			return errors.New("identity platform token provider must not be nil")
		}
		if options.platformTokenProvider != nil {
			return errors.New("identity platform token provider is already configured")
		}

		options.platformTokenProvider = provider
		return nil
	}
}

type googleIDTokenProvider struct {
	provider auth.TokenProvider
}

func newGoogleIDTokenProvider(audience string) (*googleIDTokenProvider, error) {
	audience = strings.TrimSpace(audience)
	if audience == "" {
		return nil, errors.New("google ID token audience must not be empty")
	}

	credentials, err := googleidtoken.NewCredentials(
		&googleidtoken.Options{
			Audience: audience,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("create Google ID token credentials: %w", err)
	}

	// Keep one token per audience and refresh synchronously when required.
	// Synchronous refresh ensures the caller's context bounds metadata access.
	cachedProvider := auth.NewCachedTokenProvider(
		credentials,
		&auth.CachedTokenProviderOptions{
			DisableAsyncRefresh: true,
		},
	)

	return &googleIDTokenProvider{
		provider: cachedProvider,
	}, nil
}

func (p *googleIDTokenProvider) Token(ctx context.Context) (string, error) {
	if p == nil || p.provider == nil {
		return "", errors.New("google ID token provider is not configured")
	}

	token, err := p.provider.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("acquire Google ID token: %w", err)
	}
	if token == nil {
		return "", errors.New("google ID token provider returned no token")
	}

	value := strings.TrimSpace(token.Value)
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return "", errors.New("google ID token provider returned an invalid token")
	}

	return value, nil
}
