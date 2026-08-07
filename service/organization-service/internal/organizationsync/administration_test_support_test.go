package organizationsync

import (
	"context"

	"github.com/google/uuid"
)

func (u *fakeUnitOfWork) GetPendingInvitationIntent(context.Context, uuid.UUID, uuid.UUID) (InvitationIntent, bool, error) {
	return InvitationIntent{}, false, nil
}

func (u *fakeUnitOfWork) ConsumeInvitationIntent(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}

func (u *fakeUnitOfWork) DeleteMembershipRemovalIntent(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (u *stagedUnitOfWork) GetPendingInvitationIntent(context.Context, uuid.UUID, uuid.UUID) (InvitationIntent, bool, error) {
	return InvitationIntent{}, false, nil
}

func (u *stagedUnitOfWork) ConsumeInvitationIntent(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}

func (u *stagedUnitOfWork) DeleteMembershipRemovalIntent(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}
