package clerkorganizationadmin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationmaintenance"
)

const membershipPresenceResponseLimit = int64(64 * 1024)

type membershipPresenceResponse struct {
	Data []struct {
		PublicUserData *struct {
			UserID string `json:"user_id"`
		} `json:"public_user_data"`
	} `json:"data"`
}

func (c *Client) MembershipExists(
	ctx context.Context,
	clerkOrganizationID string,
	clerkUserID string,
) (bool, error) {
	if c == nil || c.httpClient == nil || c.backendURL == "" || c.secretKey == "" {
		return false, organizationmaintenance.ErrProviderUnavailable
	}
	clerkOrganizationID = strings.TrimSpace(clerkOrganizationID)
	clerkUserID = strings.TrimSpace(clerkUserID)
	if clerkOrganizationID == "" || clerkUserID == "" {
		return false, organizationmaintenance.ErrProviderUnavailable
	}

	requestContext, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	endpoint, err := url.Parse(c.backendURL + "/organizations/" + url.PathEscape(clerkOrganizationID) + "/memberships")
	if err != nil {
		return false, organizationmaintenance.ErrProviderUnavailable
	}
	query := endpoint.Query()
	query.Set("user_id", clerkUserID)
	query.Set("limit", "1")
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return false, organizationmaintenance.ErrProviderUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+c.secretKey)
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return false, organizationmaintenance.ErrProviderUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return false, organizationmaintenance.ErrProviderUnavailable
	}
	if response.ContentLength > membershipPresenceResponseLimit {
		return false, organizationmaintenance.ErrProviderUnavailable
	}

	decoder := json.NewDecoder(io.LimitReader(response.Body, membershipPresenceResponseLimit+1))
	var payload membershipPresenceResponse
	if err := decoder.Decode(&payload); err != nil {
		return false, organizationmaintenance.ErrProviderUnavailable
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return false, organizationmaintenance.ErrProviderUnavailable
	}
	if len(payload.Data) == 0 {
		return false, nil
	}
	if len(payload.Data) != 1 || payload.Data[0].PublicUserData == nil || payload.Data[0].PublicUserData.UserID != clerkUserID {
		return false, organizationmaintenance.ErrProviderUnavailable
	}
	return true, nil
}
