package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationonboarding"
)

const (
	onboardingCommandTimeout = 3 * time.Second
	onboardingMaxBodyBytes   = int64(16 * 1024)
)

type OrganizationOnboarding interface {
	UpdateProfile(context.Context, authorization.ActorContext, organizationonboarding.ProfilePatch) (currentorganization.Organization, error)
	RequestVerification(context.Context, authorization.ActorContext) (currentorganization.Organization, error)
}

type nullableString struct {
	Set   bool
	Value *string
}

func (value *nullableString) UnmarshalJSON(data []byte) error {
	value.Set = true
	if string(data) == "null" {
		value.Value = nil
		return nil
	}
	var decoded string
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	value.Value = &decoded
	return nil
}

type profilePatchRequest struct {
	LegalName   nullableString `json:"legal_name"`
	Website     nullableString `json:"website"`
	Country     nullableString `json:"country"`
	CompanyType nullableString `json:"company_type"`
}

func PatchCurrentOrganization(resolver CurrentOrganizationResolver, onboarding OrganizationOnboarding) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, ok := resolveActor(w, r, resolver)
		if !ok {
			return
		}

		var request profilePatchRequest
		if err := decodeBoundedJSON(w, r, &request); err != nil {
			writeOnboardingDecodeError(w, r, err)
			return
		}
		patch := organizationonboarding.ProfilePatch{
			LegalName: organizationonboarding.StringPatch{
				Set: request.LegalName.Set, Value: request.LegalName.Value,
			},
			Website: organizationonboarding.StringPatch{
				Set: request.Website.Set, Value: request.Website.Value,
			},
			Country: organizationonboarding.StringPatch{
				Set: request.Country.Set, Value: request.Country.Value,
			},
			CompanyType: organizationonboarding.StringPatch{
				Set: request.CompanyType.Set, Value: request.CompanyType.Value,
			},
		}

		ctx, cancel := context.WithTimeout(r.Context(), onboardingCommandTimeout)
		defer cancel()
		organization, err := onboarding.UpdateProfile(ctx, result.Actor, patch)
		if err != nil {
			writeOnboardingError(w, r, err)
			return
		}
		writeOrganizationResponse(w, organization)
	}
}

func RequestCurrentOrganizationVerification(resolver CurrentOrganizationResolver, onboarding OrganizationOnboarding) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, ok := resolveActor(w, r, resolver)
		if !ok {
			return
		}

		var request struct{}
		if err := decodeOptionalEmptyJSON(w, r, &request); err != nil {
			writeOnboardingDecodeError(w, r, err)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), onboardingCommandTimeout)
		defer cancel()
		organization, err := onboarding.RequestVerification(ctx, result.Actor)
		if err != nil {
			writeOnboardingError(w, r, err)
			return
		}
		writeOrganizationResponse(w, organization)
	}
}

func decodeBoundedJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, onboardingMaxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func decodeOptionalEmptyJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, onboardingMaxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func writeOnboardingDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeError(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
		return
	}
	writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid request body")
}

func writeOnboardingError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, organizationonboarding.ErrInvalidProfile):
		writeError(w, r, http.StatusBadRequest, "invalid_organization_profile", "invalid organization profile")
	case errors.Is(err, organizationonboarding.ErrVerificationTransitionNotAllowed):
		writeError(w, r, http.StatusConflict, "verification_transition_not_allowed", "verification request is not allowed")
	default:
		writeActorError(w, r, err)
	}
}
