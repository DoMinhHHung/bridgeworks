package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationaudit"
)

type OrganizationAudit interface {
	List(context.Context, authorization.ActorContext, int, string) (organizationaudit.Page, error)
}

type auditEventsResponse struct {
	Items      []auditEventResponse `json:"items"`
	NextCursor *string              `json:"next_cursor"`
}

type auditEventResponse struct {
	ID                  string  `json:"id"`
	EventType           string  `json:"event_type"`
	ActorKind           string  `json:"actor_kind"`
	SubjectMembershipID *string `json:"subject_membership_id"`
	FromValue           *string `json:"from_value"`
	ToValue             *string `json:"to_value"`
	OccurredAt          string  `json:"occurred_at"`
}

func CurrentOrganizationAuditEvents(
	resolver CurrentOrganizationResolver,
	audit OrganizationAudit,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, ok := resolveActor(w, r, resolver)
		if !ok {
			return
		}
		limit, err := parseBoundedLimit(r.URL.Query().Get("limit"), organizationaudit.DefaultPageSize, organizationaudit.MaxPageSize)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_pagination", "invalid pagination")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), actorResolutionTimeout)
		defer cancel()
		page, err := audit.List(ctx, result.Actor, limit, r.URL.Query().Get("cursor"))
		if err != nil {
			switch {
			case errors.Is(err, currentorganization.ErrPermissionDenied):
				writeActorError(w, r, err)
			case errors.Is(err, organizationaudit.ErrInvalidCursor):
				writeError(w, r, http.StatusBadRequest, "invalid_pagination", "invalid pagination")
			default:
				writeError(w, r, http.StatusServiceUnavailable, "service_unavailable", "service temporarily unavailable")
			}
			return
		}
		items := make([]auditEventResponse, 0, len(page.Items))
		for _, event := range page.Items {
			var subject *string
			if event.SubjectMembershipID != nil {
				value := event.SubjectMembershipID.String()
				subject = &value
			}
			items = append(items, auditEventResponse{
				ID:                  event.ID.String(),
				EventType:           event.EventType,
				ActorKind:           event.ActorKind,
				SubjectMembershipID: subject,
				FromValue:           event.FromValue,
				ToValue:             event.ToValue,
				OccurredAt:          event.OccurredAt.UTC().Format(time.RFC3339Nano),
			})
		}
		writeJSON(w, http.StatusOK, auditEventsResponse{Items: items, NextCursor: page.NextCursor})
	}
}
