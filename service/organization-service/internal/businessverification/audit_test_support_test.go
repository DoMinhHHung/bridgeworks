package businessverification

import (
	"context"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationaudit"
)

func (u *fakeBusinessUOW) InsertAuditEvent(context.Context, organizationaudit.Event) error {
	return nil
}
