package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platformauthorization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platformreview"
	"github.com/go-chi/chi/v5"
)

const (
	platformReviewTimeout   = 5 * time.Second
	platformDecisionMaxBody = int64(1024)
)

type PlatformReview interface {
	AuthorizeReviewer(context.Context, string, string) (platformreview.Reviewer, error)
	ListQueue(context.Context, platformreview.Reviewer, int, string) (platformreview.QueuePage, error)
	Decide(context.Context, platformreview.Reviewer, string, string) (currentorganization.Organization, error)
}

type verificationQueueResponse struct {
	Items      []verificationQueueItemResponse `json:"items"`
	NextCursor *string                         `json:"next_cursor"`
}

type verificationQueueItemResponse struct {
	ID                      string     `json:"id"`
	Name                    *string    `json:"name"`
	LegalName               *string    `json:"legal_name"`
	Website                 *string    `json:"website"`
	Country                 *string    `json:"country"`
	CompanyType             *string    `json:"company_type"`
	VerificationStatus      string     `json:"verification_status"`
	BusinessEmailDomain     *string    `json:"business_email_domain"`
	BusinessEmailVerifiedAt *time.Time `json:"business_email_verified_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
	RequestedAt             time.Time  `json:"requested_at"`
}

type verificationDecisionRequest struct {
	Decision string `json:"decision"`
}

func PlatformVerificationQueue(service PlatformReview) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reviewer, ok := authorizePlatformReviewer(w, r, service)
		if !ok {
			return
		}
		limit, err := parseBoundedLimit(r.URL.Query().Get("limit"), platformreview.DefaultPageSize, platformreview.MaxPageSize)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_pagination", "invalid pagination")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), platformReviewTimeout)
		defer cancel()
		page, err := service.ListQueue(ctx, reviewer, limit, r.URL.Query().Get("cursor"))
		if err != nil {
			writePlatformReviewError(w, r, err)
			return
		}
		items := make([]verificationQueueItemResponse, 0, len(page.Items))
		for _, item := range page.Items {
			items = append(items, verificationQueueItemResponse{
				ID:                      item.ID.String(),
				Name:                    item.Name,
				LegalName:               item.LegalName,
				Website:                 item.Website,
				Country:                 item.Country,
				CompanyType:             item.CompanyType,
				VerificationStatus:      item.VerificationStatus,
				BusinessEmailDomain:     item.BusinessEmailDomain,
				BusinessEmailVerifiedAt: item.BusinessEmailVerifiedAt,
				UpdatedAt:               item.UpdatedAt,
				RequestedAt:             item.RequestedAt,
			})
		}
		writeJSON(w, http.StatusOK, verificationQueueResponse{Items: items, NextCursor: page.NextCursor})
	}
}

func PlatformVerificationDecision(service PlatformReview) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reviewer, ok := authorizePlatformReviewer(w, r, service)
		if !ok {
			return
		}
		var request verificationDecisionRequest
		if err := decodeStrictJSON(w, r, platformDecisionMaxBody, &request); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_verification_decision", "invalid verification decision")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), platformReviewTimeout)
		defer cancel()
		organization, err := service.Decide(ctx, reviewer, chi.URLParam(r, "organizationID"), request.Decision)
		if err != nil {
			writePlatformReviewError(w, r, err)
			return
		}
		writeOrganizationResponse(w, organization)
	}
}

func authorizePlatformReviewer(w http.ResponseWriter, r *http.Request, service PlatformReview) (platformreview.Reviewer, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), actorResolutionTimeout)
	defer cancel()
	reviewer, err := service.AuthorizeReviewer(
		ctx,
		r.Header.Get("Authorization"),
		RequestIDFromContext(r.Context()),
	)
	if err != nil {
		writePlatformReviewError(w, r, err)
		return platformreview.Reviewer{}, false
	}
	return reviewer, true
}

func writePlatformReviewError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, currentorganization.ErrUnauthorized), errors.Is(err, platformauthorization.ErrUnauthorized):
		w.Header().Set("WWW-Authenticate", `Bearer realm="bridgeworks"`)
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "authentication required")
	case errors.Is(err, currentorganization.ErrAccountDisabled),
		errors.Is(err, currentorganization.ErrAccountDeleted),
		errors.Is(err, platformauthorization.ErrAccountInactive),
		errors.Is(err, platformauthorization.ErrPermissionDenied):
		writeError(w, r, http.StatusForbidden, "platform_permission_required", "platform verification review permission required")
	case errors.Is(err, currentorganization.ErrIdentityNotReady),
		errors.Is(err, platformauthorization.ErrIdentityNotReady),
		errors.Is(err, platformauthorization.ErrUnavailable):
		writeError(w, r, http.StatusServiceUnavailable, "service_unavailable", "service temporarily unavailable")
	case errors.Is(err, platformreview.ErrInvalidDecision):
		writeError(w, r, http.StatusBadRequest, "invalid_verification_decision", "invalid verification decision")
	case errors.Is(err, platformreview.ErrInvalidCursor):
		writeError(w, r, http.StatusBadRequest, "invalid_pagination", "invalid pagination")
	case errors.Is(err, platformreview.ErrOrganizationNotFound):
		writeError(w, r, http.StatusNotFound, "organization_not_found", "organization not found")
	case errors.Is(err, platformreview.ErrOrganizationNotReviewable):
		writeError(w, r, http.StatusConflict, "organization_not_reviewable", "organization is not reviewable")
	case errors.Is(err, platformreview.ErrVerificationNotPending):
		writeError(w, r, http.StatusConflict, "verification_not_pending", "organization verification is not pending")
	case errors.Is(err, platformreview.ErrDecisionConflict):
		writeError(w, r, http.StatusConflict, "verification_decision_conflict", "verification decision conflicts with current state")
	default:
		writeError(w, r, http.StatusServiceUnavailable, "service_unavailable", "service temporarily unavailable")
	}
}

func parseBoundedLimit(raw string, fallback, maximum int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 || value > maximum {
		return 0, errors.New("invalid limit")
	}
	return value, nil
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}
