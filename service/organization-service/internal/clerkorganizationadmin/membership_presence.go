package clerkorganizationadmin

import (
	"context"
	"errors"
	"net/http"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationmaintenance"
	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/organizationmembership"
)

func (c *Client) MembershipExists(
	ctx context.Context,
	clerkOrganizationID string,
	clerkUserID string,
) (bool, error) {
	requestContext, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	params := &organizationmembership.ListParams{
		OrganizationID: clerkOrganizationID,
		UserIDs:        []string{clerkUserID},
	}
	params.Limit = clerk.Int64(1)
	list, err := c.memberships.List(requestContext, params)
	if err != nil {
		return false, classifyPresenceError(err)
	}
	if list == nil {
		return false, organizationmaintenance.ErrProviderUnavailable
	}
	if len(list.OrganizationMemberships) == 0 {
		return false, nil
	}
	for _, membership := range list.OrganizationMemberships {
		if membership == nil || membership.PublicUserData == nil {
			return false, organizationmaintenance.ErrProviderUnavailable
		}
		if membership.PublicUserData.UserID == clerkUserID {
			return true, nil
		}
	}
	return false, organizationmaintenance.ErrProviderUnavailable
}

func classifyPresenceError(err error) error {
	if err == nil {
		return nil
	}
	if isNetworkUnavailable(err) {
		return organizationmaintenance.ErrProviderUnavailable
	}
	var apiErr *clerk.APIErrorResponse
	if !errors.As(err, &apiErr) {
		return organizationmaintenance.ErrProviderUnavailable
	}
	if apiErr.HTTPStatusCode == http.StatusNotFound {
		return nil
	}
	return organizationmaintenance.ErrProviderUnavailable
}
