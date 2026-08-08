package main

import (
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

const maxBodyBytes = int64(16 * 1024)

type invitationRequest struct {
	EmailAddress   string         `json:"email_address"`
	Role           string         `json:"role"`
	InviterUserID  string         `json:"inviter_user_id"`
	PublicMetadata map[string]any `json:"public_metadata"`
}

func main() {
	addr := flag.String("addr", ":8081", "listen address")
	flag.Parse()
	secret := strings.TrimSpace(os.Getenv("CLERK_BACKEND_MOCK_SECRET"))
	if secret == "" {
		panic("CLERK_BACKEND_MOCK_SECRET is required")
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	server := &http.Server{
		Addr:              *addr,
		Handler:           handler(secret),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	logger.Info("clerk backend mock starting")
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		logger.Error("clerk backend mock stopped", "error", err)
		os.Exit(1)
	}
}

func handler(secret string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			writeError(w, http.StatusUnauthorized)
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 4 || parts[0] != "v1" || parts[1] != "organizations" || strings.TrimSpace(parts[2]) == "" {
			writeError(w, http.StatusNotFound)
			return
		}
		switch {
		case r.Method == http.MethodPost && len(parts) == 4 && parts[3] == "invitations":
			handleInvitation(w, r, parts[2])
		case r.Method == http.MethodGet && len(parts) == 4 && parts[3] == "memberships":
			handleListMemberships(w, r, parts[2])
		case r.Method == http.MethodDelete && len(parts) == 5 && parts[3] == "memberships" && strings.TrimSpace(parts[4]) != "":
			handleDeleteMembership(w, parts[2], parts[4])
		default:
			writeError(w, http.StatusNotFound)
		}
	})
}

func handleInvitation(w http.ResponseWriter, r *http.Request, organizationID string) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	var request invitationRequest
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest)
		return
	}
	if request.Role != "org:member" || strings.TrimSpace(request.EmailAddress) == "" || strings.TrimSpace(request.InviterUserID) == "" {
		writeError(w, http.StatusUnprocessableEntity)
		return
	}
	intent, ok := request.PublicMetadata["bridgeworks_invitation_id"].(string)
	if !ok || strings.TrimSpace(intent) == "" {
		writeError(w, http.StatusUnprocessableEntity)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": "inv_mock", "object": "organization_invitation",
		"email_address": request.EmailAddress, "role": request.Role,
		"status": "pending", "organization_id": organizationID,
		"public_metadata": request.PublicMetadata,
		"created_at": 1, "updated_at": 1,
	})
}

func handleListMemberships(w http.ResponseWriter, r *http.Request, organizationID string) {
	userIDs := r.URL.Query()["user_id"]
	if len(userIDs) != 1 || strings.TrimSpace(userIDs[0]) == "" {
		writeError(w, http.StatusBadRequest)
		return
	}
	userID := userIDs[0]
	if strings.Contains(userID, "outage") {
		writeError(w, http.StatusServiceUnavailable)
		return
	}
	memberships := []map[string]any{}
	if !strings.Contains(userID, "absent") {
		memberships = append(memberships, membershipPayload(organizationID, userID))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list", "data": memberships, "total_count": len(memberships),
	})
}

func handleDeleteMembership(w http.ResponseWriter, organizationID, userID string) {
	writeJSON(w, http.StatusOK, membershipPayload(organizationID, userID))
}

func membershipPayload(organizationID, userID string) map[string]any {
	return map[string]any{
		"id": "mem_mock", "object": "organization_membership",
		"organization": map[string]any{"id": organizationID, "name": "mock", "slug": "mock", "max_allowed_memberships": 10},
		"public_user_data": map[string]any{"user_id": userID, "identifier": nil, "first_name": nil, "last_name": nil, "image_url": "", "has_image": false},
		"role": "org:member", "permissions": []string{}, "public_metadata": map[string]any{}, "private_metadata": map[string]any{},
		"created_at": 1, "updated_at": 1,
	}
}

func writeError(w http.ResponseWriter, status int) {
	writeJSON(w, status, map[string]any{
		"errors": []map[string]string{{"message": http.StatusText(status)}},
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
