package identityclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform/safeerr"
	"github.com/google/uuid"
)

const maxResponseBodyBytes = int64(64 * 1024)

type Client struct {
	baseURL               *url.URL
	client                *http.Client
	requestTimeout        time.Duration
	platformTokenProvider platformTokenProvider
}

type currentUserResponse struct {
	ID string `json:"id"`
}

type errorResponse struct {
	Code string `json:"code"`
}

func New(rawURL string, timeout time.Duration, options ...Option) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("identity service URL must be absolute")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("identity service URL scheme must be HTTP or HTTPS")
	}
	if timeout <= 0 || timeout > 5*time.Second {
		return nil, errors.New(
			"identity service timeout must be between zero and five seconds",
		)
	}

	resolvedOptions := clientOptions{}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("identity client option must not be nil")
		}
		if err := option(&resolvedOptions); err != nil {
			return nil, err
		}
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/")

	transport := &http.Transport{
		// This request forwards the user's Bearer token to a configured private
		// dependency. Ambient HTTP proxy variables must not redirect that token.
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   min(timeout, time.Second),
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       90 * time.Second,
	}

	return &Client{
		baseURL:               parsed,
		requestTimeout:        timeout,
		platformTokenProvider: resolvedOptions.platformTokenProvider,
		client: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
	}, nil
}

func (c *Client) Resolve(
	ctx context.Context,
	authorizationHeader string,
	requestID string,
) (currentorganization.Identity, error) {
	requestContext, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/me"

	req, err := http.NewRequestWithContext(
		requestContext,
		http.MethodGet,
		endpoint.String(),
		nil,
	)
	if err != nil {
		return currentorganization.Identity{},
			safeerr.Wrap("create identity request", err)
	}

	if c.platformTokenProvider != nil {
		platformToken, err := c.platformTokenProvider.Token(requestContext)
		if err != nil {
			return currentorganization.Identity{},
				safeerr.Wrap("acquire identity platform token", err)
		}

		req.Header.Set(
			"X-Serverless-Authorization",
			"Bearer "+platformToken,
		)
	}

	req.Header.Set("Authorization", authorizationHeader)

	if requestID != "" {
		req.Header.Set("X-Request-Id", requestID)
	}

	response, err := c.client.Do(req)
	if err != nil {
		return currentorganization.Identity{},
			safeerr.Wrap("call identity service", err)
	}

	defer func() {
		_ = response.Body.Close()
	}()

	body, err := io.ReadAll(
		io.LimitReader(response.Body, maxResponseBodyBytes+1),
	)
	if err != nil {
		return currentorganization.Identity{},
			safeerr.Wrap("read identity response", err)
	}
	if int64(len(body)) > maxResponseBodyBytes {
		return currentorganization.Identity{},
			errors.New("identity response exceeded maximum size")
	}

	switch response.StatusCode {
	case http.StatusOK:
		var payload currentUserResponse
		if err := json.Unmarshal(body, &payload); err != nil {
			return currentorganization.Identity{},
				safeerr.Wrap("decode identity response", err)
		}

		identityID, err := uuid.Parse(payload.ID)
		if err != nil {
			return currentorganization.Identity{},
				safeerr.Wrap("parse identity user ID", err)
		}

		return currentorganization.Identity{
			ID: identityID,
		}, nil

	case http.StatusUnauthorized:
		if decodeErrorCode(body) == "unauthorized" {
			return currentorganization.Identity{},
				currentorganization.ErrUnauthorized
		}

		return currentorganization.Identity{},
			errors.New("unexpected identity unauthorized response")

	case http.StatusForbidden:
		code := decodeErrorCode(body)
		switch code {
		case "account_disabled":
			return currentorganization.Identity{},
				currentorganization.ErrAccountDisabled
		case "account_deleted":
			return currentorganization.Identity{},
				currentorganization.ErrAccountDeleted
		default:
			return currentorganization.Identity{},
				errors.New("unexpected identity forbidden response")
		}

	case http.StatusConflict:
		if decodeErrorCode(body) == "identity_not_ready" {
			return currentorganization.Identity{},
				currentorganization.ErrIdentityNotReady
		}

		return currentorganization.Identity{},
			errors.New("unexpected identity conflict response")

	case http.StatusServiceUnavailable:
		return currentorganization.Identity{},
			errors.New("identity service unavailable")

	default:
		return currentorganization.Identity{},
			fmt.Errorf("unexpected identity status: %d", response.StatusCode)
	}
}

func decodeErrorCode(body []byte) string {
	var payload errorResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}

	return strings.TrimSpace(payload.Code)
}

func (c *Client) CloseIdleConnections() {
	if c == nil || c.client == nil {
		return
	}

	if transport, ok := c.client.Transport.(interface{ CloseIdleConnections() }); ok {
		transport.CloseIdleConnections()
	}
}
