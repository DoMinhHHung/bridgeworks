package organizationsync

import (
	"context"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationid"
)

type Repository interface {
	ProcessEvent(context.Context, Event, organizationid.Generator) error
}
type Service struct {
	repository Repository
	generator  organizationid.Generator
}

func New(repository Repository, generator organizationid.Generator) *Service {
	return &Service{repository: repository, generator: generator}
}
func (s *Service) Process(ctx context.Context, event Event) error {
	return s.repository.ProcessEvent(ctx, event, s.generator)
}
