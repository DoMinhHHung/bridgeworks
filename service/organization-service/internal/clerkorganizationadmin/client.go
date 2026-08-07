package clerkorganizationadmin

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/membershipadmin"
	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/organizationinvitation"
	"github.com/clerk/clerk-sdk-go/v2/organizationmembership"
)

const providerMembershipRole = "org:member"

type Client struct {
	invitations    *organizationinvitation.Client
	memberships    *organizationmembership.Client
	httpClient     *http.Client
	requestTimeout time.Duration
}

func New(secretKey, rawURL string, timeout time.Duration) (*Client, error) {
	secretKey = strings.TrimSpace(secretKey)
	if secretKey == "" {
		return nil, errors.New("Clerk secret key is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("Clerk Backend API URL must be absolute")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("Clerk Backend API URL scheme must be HTTP or HTTPS")
	}
	if timeout <= 0 || timeout > 5*time.Second {
		return nil, errors.New("Clerk Backend API timeout must be between zero and five seconds")
	}
	baseURL := strings.TrimRight(parsed.String(), "/")
	transport := &http.Transport{
		// The Clerk secret must never be redirected through ambient proxy state.
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   min(timeout, time.Second),
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       90 * time.Second,
	}
	httpClient := &http.Client{Transport: transport, Timeout: timeout}
	config := &clerk.ClientConfig{BackendConfig: clerk.BackendConfig{
		HTTPClient: httpClient,
		URL:        clerk.String(baseURL),
		Key:        clerk.String(secretKey),
	}}
	return &Client{
		invitations:    organizationinvitation.NewClient(config),
		memberships:    organizationmembership.NewClient(config),
		httpClient:     httpClient,
		requestTimeout: timeout,
	}, nil
}

func (c *Client) CreateInvitation(
	ctx context.Context,
	request membershipadmin.InvitationProviderRequest,
) error {
	metadata, err := json.Marshal(map[string]string{
		"bridgeworks_invitation_id": request.InvitationIntentID.String(),
	})
	if err != nil {
		return membershipadmin.ProviderErrRejected
	}
	requestContext, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	_, err = c.invitations.Create(requestContext, &organizationinvitation.CreateParams{
		OrganizationID: request.ClerkOrganizationID,
		EmailAddress:   clerk.String(request.EmailAddress),
		Role:           clerk.String(providerMembershipRole),
		InviterUserID:  clerk.String(request.ClerkInviterUserID),
		PublicMetadata: clerk.JSONRawMessage(metadata),
	})
	return classifyCreateError(err)
}

func (c *Client) DeleteMembership(
	ctx context.Context,
	request membershipadmin.MembershipDeleteProviderRequest,
) error {
	requestContext, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	_, err := c.memberships.Delete(requestContext, &organizationmembership.DeleteParams{
		OrganizationID: request.ClerkOrganizationID,
		UserID:         request.ClerkUserID,
	})
	return classifyDeleteError(err)
}

func (c *Client) CloseIdleConnections() {
	if c == nil || c.httpClient == nil {
		return
	}
	c.httpClient.CloseIdleConnections()
}

func classifyCreateError(err error) error {
	if err == nil {
		return nil
	}
	if isNetworkUnavailable(err) {
		return membershipadmin.ProviderErrUnavailable
	}
	var apiErr *clerk.APIErrorResponse
	if !errors.As(err, &apiErr) {
		return membershipadmin.ProviderErrUnavailable
	}
	switch apiErr.HTTPStatusCode {
	case http.StatusConflict:
		return membershipadmin.ProviderErrConflict
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity:
		return membershipadmin.ProviderErrRejected
	case http.StatusTooManyRequests:
		return membershipadmin.ProviderErrUnavailable
	default:
		if apiErr.HTTPStatusCode >= 500 {
			return membershipadmin.ProviderErrUnavailable
		}
		return membershipadmin.ProviderErrRejected
	}
}

func classifyDeleteError(err error) error {
	if err == nil {
		return nil
	}
	if isNetworkUnavailable(err) {
		return membershipadmin.ProviderErrUnavailable
	}
	var apiErr *clerk.APIErrorResponse
	if !errors.As(err, &apiErr) {
		return membershipadmin.ProviderErrUnavailable
	}
	switch apiErr.HTTPStatusCode {
	case http.StatusNotFound:
		return membershipadmin.ProviderErrNotFound
	case http.StatusConflict, http.StatusUnprocessableEntity:
		return membershipadmin.ProviderErrConflict
	case http.StatusTooManyRequests:
		return membershipadmin.ProviderErrUnavailable
	default:
		if apiErr.HTTPStatusCode >= 500 {
			return membershipadmin.ProviderErrUnavailable
		}
		return membershipadmin.ProviderErrRejected
	}
}

func isNetworkUnavailable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}
