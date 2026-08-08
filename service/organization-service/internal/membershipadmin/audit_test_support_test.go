package membershipadmin

import (
	"context"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationaudit"
)

func (u *fakeAdminUOW) InsertAuditEvent(context.Context, organizationaudit.Event) error {
	return nil
}
