package identityclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform/safeerr"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platformauthorization"
)

type platformAccessResponse struct {
	Roles       *[]string `json:"roles"`
	Permissions *[]string `json:"permissions"`
}

func (c *Client) ResolvePlatformAccess(
	ctx context.Context,
	authorizationHeader string,
	requestID string,
) (platformauthorization.Access, error) {
	if c == nil || c.client == nil || c.baseURL == nil {
		return platformauthorization.Access{}, platformauthorization.ErrUnavailable
	}

	requestContext, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/internal/v1/platform-access/me"
	req, err := http.NewRequestWithContext(requestContext, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return platformauthorization.Access{}, safeerr.Wrap("create Identity platform access request", err)
	}
	if c.platformTokenProvider != nil {
		platformToken, err := c.platformTokenProvider.Token(requestContext)
		if err != nil {
			return platformauthorization.Access{}, safeerr.Wrap("acquire Identity platform token", err)
		}
		req.Header.Set("X-Serverless-Authorization", "Bearer "+platformToken)
	}
	if strings.TrimSpace(authorizationHeader) == "" {
		return platformauthorization.Access{}, platformauthorization.ErrUnauthorized
	}
	req.Header.Set("Authorization", authorizationHeader)
	if requestID != "" {
		req.Header.Set("X-Request-Id", requestID)
	}

	response, err := c.client.Do(req)
	if err != nil {
		return platformauthorization.Access{}, safeerr.Wrap("call Identity platform access", err)
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodyBytes+1))
	if err != nil {
		return platformauthorization.Access{}, safeerr.Wrap("read Identity platform access response", err)
	}
	if int64(len(body)) > maxResponseBodyBytes {
		return platformauthorization.Access{}, platformauthorization.ErrUnavailable
	}

	switch response.StatusCode {
	case http.StatusOK:
		return decodePlatformAccess(body)
	case http.StatusUnauthorized:
		if decodeErrorCode(body) == "unauthorized" {
			return platformauthorization.Access{}, platformauthorization.ErrUnauthorized
		}
	case http.StatusForbidden:
		code := decodeErrorCode(body)
		if code == "account_disabled" || code == "account_deleted" {
			return platformauthorization.Access{}, platformauthorization.ErrAccountInactive
		}
	case http.StatusConflict:
		if decodeErrorCode(body) == "identity_not_ready" {
			return platformauthorization.Access{}, platformauthorization.ErrIdentityNotReady
		}
	}
	return platformauthorization.Access{}, platformauthorization.ErrUnavailable
}

func decodePlatformAccess(body []byte) (platformauthorization.Access, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var payload platformAccessResponse
	if err := decoder.Decode(&payload); err != nil {
		return platformauthorization.Access{}, platformauthorization.ErrUnavailable
	}
	if payload.Roles == nil || payload.Permissions == nil {
		return platformauthorization.Access{}, platformauthorization.ErrUnavailable
	}
	if err := ensurePlatformAccessEOF(decoder); err != nil {
		return platformauthorization.Access{}, platformauthorization.ErrUnavailable
	}
	return platformauthorization.Access{
		Roles:       append([]string(nil), (*payload.Roles)...),
		Permissions: append([]string(nil), (*payload.Permissions)...),
	}, nil
}

func ensurePlatformAccessEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("multiple JSON values in Identity platform access response")
}
