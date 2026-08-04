package authn

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platform/safeerr"
	"github.com/clerk/clerk-sdk-go/v2"
	clerkhttp "github.com/clerk/clerk-sdk-go/v2/http"
)

const (
	unauthorizedCode           = "unauthorized"
	unauthorizedMessage        = "authentication required"
	wwwAuthenticateHeaderValue = `Bearer realm="bridgeworks"`
)

type Principal struct {
	ClerkUserID string
	SessionID   string
}

type Config struct {
	JWTKey            string
	Issuer            string
	AuthorizedParties []string
	Leeway            time.Duration
}

type RequestIDFunc func(context.Context) string

type principalContextKey struct{}

type errorEnvelope struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Details   any    `json:"details"`
}

func New(
	config Config,
	logger *slog.Logger,
	requestID RequestIDFunc,
) (func(http.Handler) http.Handler, error) {
	config.JWTKey = strings.TrimSpace(config.JWTKey)
	config.Issuer = strings.TrimSpace(config.Issuer)
	if config.JWTKey == "" {
		return nil, errors.New("clerk JWT key is required")
	}
	if config.Issuer == "" {
		return nil, errors.New("clerk issuer is required")
	}
	if len(config.AuthorizedParties) == 0 {
		return nil, errors.New("at least one clerk authorized party is required")
	}
	if config.Leeway <= 0 {
		return nil, errors.New("clerk authentication leeway must be greater than zero")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if requestID == nil {
		requestID = func(context.Context) string { return "" }
	}

	validationParams := &clerkhttp.AuthorizationParams{}
	if err := clerkhttp.JSONWebKey(config.JWTKey)(validationParams); err != nil {
		return nil, safeerr.Wrap("initialize Clerk JWT verifier", err)
	}

	authorizedParties := make(map[string]struct{}, len(config.AuthorizedParties))
	parties := make([]string, 0, len(config.AuthorizedParties))
	for _, party := range config.AuthorizedParties {
		party = strings.TrimSpace(party)
		if party == "" {
			return nil, errors.New("clerk authorized party must not be empty")
		}
		if _, exists := authorizedParties[party]; exists {
			return nil, errors.New("clerk authorized party must be unique")
		}
		authorizedParties[party] = struct{}{}
		parties = append(parties, party)
	}

	reject := func(w http.ResponseWriter, r *http.Request, reason string) {
		logger.WarnContext(
			r.Context(),
			"request authentication rejected",
			"request_id", requestID(r.Context()),
			"reason", reason,
		)
		writeUnauthorized(w, r, requestID(r.Context()))
	}

	sdkMiddleware := clerkhttp.WithHeaderAuthorization(
		clerkhttp.JSONWebKey(config.JWTKey),
		clerkhttp.AuthorizedPartyMatches(parties...),
		clerkhttp.Leeway(config.Leeway),
		clerkhttp.ProxyURL(config.Issuer),
		clerkhttp.AuthorizationJWTExtractor(func(r *http.Request) string {
			token, _ := bearerToken(r.Header.Get("Authorization"))
			return token
		}),
		clerkhttp.AuthorizationFailureHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reject(w, r, "token_invalid")
		})),
	)

	return func(next http.Handler) http.Handler {
		verifiedNext := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := clerk.SessionClaimsFromContext(r.Context())
			if !ok || claims == nil {
				reject(w, r, "token_invalid")
				return
			}
			if claims.Issuer != config.Issuer {
				reject(w, r, "issuer_invalid")
				return
			}

			clerkUserID := strings.TrimSpace(claims.Subject)
			sessionID := strings.TrimSpace(claims.SessionID)
			authorizedParty := strings.TrimSpace(claims.AuthorizedParty)
			if clerkUserID == "" || sessionID == "" || authorizedParty == "" {
				reject(w, r, "principal_invalid")
				return
			}
			if _, allowed := authorizedParties[authorizedParty]; !allowed {
				reject(w, r, "principal_invalid")
				return
			}

			ctx := ContextWithPrincipal(r.Context(), Principal{
				ClerkUserID: clerkUserID,
				SessionID:   sessionID,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})

		verified := sdkMiddleware(verifiedNext)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, valid := bearerToken(r.Header.Get("Authorization"))
			if !valid {
				reason := "token_invalid"
				if strings.TrimSpace(r.Header.Get("Authorization")) == "" {
					reason = "token_missing"
				}
				reject(w, r, reason)
				return
			}
			verified.ServeHTTP(w, r)
		})
	}, nil
}

func ContextWithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

func bearerToken(value string) (string, bool) {
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func writeUnauthorized(w http.ResponseWriter, _ *http.Request, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", wwwAuthenticateHeaderValue)
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(errorEnvelope{
		Code:      unauthorizedCode,
		Message:   unauthorizedMessage,
		RequestID: requestID,
		Details:   nil,
	})
}
