package organizationaudit

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	ActorTenantUser    = "tenant_user"
	ActorPlatformAdmin = "platform_admin"
	ActorSystem        = "system"

	EventOrganizationProfileUpdated           = "organization.profile.updated"
	EventOrganizationVerificationRequested    = "organization.verification.requested"
	EventOrganizationBusinessEmailVerified    = "organization.business_email.verified"
	EventMembershipInvitationRequested        = "membership.invitation.requested"
	EventMembershipRoleChanged                = "membership.role.changed"
	EventMembershipRemovalRequested           = "membership.removal.requested"
	EventMembershipLeaveRequested             = "membership.leave.requested"
	EventOrganizationOwnershipTransferred     = "organization.ownership.transferred"
	EventOrganizationVerificationReviewed     = "organization.verification.reviewed"
	EventMembershipActivated                  = "membership.activated"
	EventMembershipRemovalCompleted           = "membership.removal.completed"
)

type Event struct {
	ID                  uuid.UUID
	OrganizationID      uuid.UUID
	EventType           string
	ActorKind           string
	ActorIdentityUserID *uuid.UUID
	ActorMembershipID   *uuid.UUID
	SubjectMembershipID *uuid.UUID
	FromValue           *string
	ToValue             *string
	OccurredAt          time.Time
}

func TenantEvent(
	organizationID uuid.UUID,
	eventType string,
	identityUserID uuid.UUID,
	actorMembershipID uuid.UUID,
	subjectMembershipID *uuid.UUID,
	fromValue *string,
	toValue *string,
) (Event, error) {
	identity := identityUserID
	membership := actorMembershipID
	return newEvent(organizationID, eventType, ActorTenantUser, &identity, &membership, subjectMembershipID, fromValue, toValue)
}

func PlatformEvent(
	organizationID uuid.UUID,
	eventType string,
	identityUserID uuid.UUID,
	fromValue *string,
	toValue *string,
) (Event, error) {
	identity := identityUserID
	return newEvent(organizationID, eventType, ActorPlatformAdmin, &identity, nil, nil, fromValue, toValue)
}

func SystemEvent(
	organizationID uuid.UUID,
	eventType string,
	subjectMembershipID *uuid.UUID,
	fromValue *string,
	toValue *string,
) (Event, error) {
	return newEvent(organizationID, eventType, ActorSystem, nil, nil, subjectMembershipID, fromValue, toValue)
}

func newEvent(
	organizationID uuid.UUID,
	eventType string,
	actorKind string,
	actorIdentityUserID *uuid.UUID,
	actorMembershipID *uuid.UUID,
	subjectMembershipID *uuid.UUID,
	fromValue *string,
	toValue *string,
) (Event, error) {
	if organizationID == uuid.Nil {
		return Event{}, errors.New("audit organization ID is required")
	}
	if !recognizedEventType(eventType) {
		return Event{}, errors.New("unrecognized audit event type")
	}
	if !validActor(actorKind, actorIdentityUserID, actorMembershipID) {
		return Event{}, errors.New("invalid audit actor")
	}
	if !validValue(fromValue) || !validValue(toValue) {
		return Event{}, errors.New("invalid audit value")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Event{}, err
	}
	return Event{
		ID:                  id,
		OrganizationID:      organizationID,
		EventType:           eventType,
		ActorKind:           actorKind,
		ActorIdentityUserID: actorIdentityUserID,
		ActorMembershipID:   actorMembershipID,
		SubjectMembershipID: subjectMembershipID,
		FromValue:           fromValue,
		ToValue:             toValue,
		OccurredAt:          time.Now().UTC(),
	}, nil
}

func recognizedEventType(eventType string) bool {
	switch eventType {
	case EventOrganizationProfileUpdated,
		EventOrganizationVerificationRequested,
		EventOrganizationBusinessEmailVerified,
		EventMembershipInvitationRequested,
		EventMembershipRoleChanged,
		EventMembershipRemovalRequested,
		EventMembershipLeaveRequested,
		EventOrganizationOwnershipTransferred,
		EventOrganizationVerificationReviewed,
		EventMembershipActivated,
		EventMembershipRemovalCompleted:
		return true
	default:
		return false
	}
}

func validActor(kind string, identityUserID, membershipID *uuid.UUID) bool {
	switch kind {
	case ActorTenantUser:
		return identityUserID != nil && *identityUserID != uuid.Nil && membershipID != nil && *membershipID != uuid.Nil
	case ActorPlatformAdmin:
		return identityUserID != nil && *identityUserID != uuid.Nil && membershipID == nil
	case ActorSystem:
		return identityUserID == nil && membershipID == nil
	default:
		return false
	}
}

func validValue(value *string) bool {
	return value == nil || (*value != "" && len(*value) <= 64)
}
