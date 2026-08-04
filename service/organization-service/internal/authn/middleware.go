package authn

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform/safeerr"
	"github.com/clerk/clerk-sdk-go/v2"
	clerkhttp "github.com/clerk/clerk-sdk-go/v2/http"
)

const challenge = `Bearer realm="bridgeworks"`

type Config struct {
	JWTKey            string
	Issuer            string
	AuthorizedParties []string
	Leeway            time.Duration
}
type RequestIDFunc func(context.Context) string
type principalKey struct{}
type errorEnvelope struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Details   any    `json:"details"`
}

func New(cfg Config, logger *slog.Logger, requestID RequestIDFunc) (func(http.Handler) http.Handler, error) {
	cfg.JWTKey = strings.TrimSpace(cfg.JWTKey)
	cfg.Issuer = strings.TrimSpace(cfg.Issuer)
	if cfg.JWTKey == "" {
		return nil, errors.New("clerk JWT key is required")
	}
	if cfg.Issuer == "" {
		return nil, errors.New("clerk issuer is required")
	}
	if len(cfg.AuthorizedParties) == 0 {
		return nil, errors.New("at least one clerk authorized party is required")
	}
	if cfg.Leeway <= 0 {
		return nil, errors.New("clerk authentication leeway must be greater than zero")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if requestID == nil {
		requestID = func(context.Context) string { return "" }
	}
	params := &clerkhttp.AuthorizationParams{}
	if err := clerkhttp.JSONWebKey(cfg.JWTKey)(params); err != nil {
		return nil, safeerr.Wrap("initialize Clerk JWT verifier", err)
	}
	allowed := make(map[string]struct{}, len(cfg.AuthorizedParties))
	parties := make([]string, 0, len(cfg.AuthorizedParties))
	for _, raw := range cfg.AuthorizedParties {
		party := strings.TrimSpace(raw)
		if party == "" {
			return nil, errors.New("clerk authorized party must not be empty")
		}
		if _, exists := allowed[party]; exists {
			return nil, errors.New("clerk authorized party must be unique")
		}
		allowed[party] = struct{}{}
		parties = append(parties, party)
	}
	reject := func(w http.ResponseWriter, r *http.Request, reason string) {
		logger.WarnContext(r.Context(), "request authentication rejected", "request_id", requestID(r.Context()), "reason", reason)
		writeUnauthorized(w, requestID(r.Context()))
	}
	sdk := clerkhttp.WithHeaderAuthorization(
		clerkhttp.JSONWebKey(cfg.JWTKey),
		clerkhttp.AuthorizedPartyMatches(parties...),
		clerkhttp.Leeway(cfg.Leeway),
		clerkhttp.ProxyURL(cfg.Issuer),
		clerkhttp.AuthorizationJWTExtractor(func(r *http.Request) string { token, _ := bearerToken(r.Header.Get("Authorization")); return token }),
		clerkhttp.AuthorizationFailureHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reject(w, r, "token_invalid") })),
	)
	return func(next http.Handler) http.Handler {
		verifiedNext := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := clerk.SessionClaimsFromContext(r.Context())
			if !ok || claims == nil {
				reject(w, r, "token_invalid")
				return
			}
			if claims.Issuer != cfg.Issuer {
				reject(w, r, "issuer_invalid")
				return
			}
			userID := strings.TrimSpace(claims.Subject)
			sessionID := strings.TrimSpace(claims.SessionID)
			party := strings.TrimSpace(claims.AuthorizedParty)
			if userID == "" || sessionID == "" || party == "" {
				reject(w, r, "principal_invalid")
				return
			}
			if _, ok := allowed[party]; !ok {
				reject(w, r, "principal_invalid")
				return
			}
			principal := authorization.Principal{ClerkUserID: userID, SessionID: sessionID, ClerkOrganizationID: strings.TrimSpace(claims.ActiveOrganizationID)}
			next.ServeHTTP(w, r.WithContext(ContextWithPrincipal(r.Context(), principal)))
		})
		verified := sdk(verifiedNext)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := bearerToken(r.Header.Get("Authorization")); !ok {
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
func ContextWithPrincipal(ctx context.Context, principal authorization.Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}
func PrincipalFromContext(ctx context.Context) (authorization.Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(authorization.Principal)
	return principal, ok
}
func bearerToken(value string) (string, bool) {
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}
func writeUnauthorized(w http.ResponseWriter, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", challenge)
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Code: "unauthorized", Message: "authentication required", RequestID: requestID, Details: nil})
}
