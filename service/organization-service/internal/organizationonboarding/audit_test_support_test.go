package organizationonboarding

import (
	"context"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationaudit"
)

func (u *fakeUnitOfWork) InsertAuditEvent(context.Context, organizationaudit.Event) error {
	return nil
}
