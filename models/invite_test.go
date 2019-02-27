package models_test

import (
	"time"

	"github.com/gofrs/uuid"
	"github.com/iansmith/mazarin/models"
)

func (ms *ModelSuite) Test_InviteCreate() {
	i := &models.Invite{
		ExpiresAt:  time.Now().Add(models.InviteLiveTime),
		InviteCode: uuid.Must(uuid.NewV4()),
		Used:       false,
	}
	verrs, err := ms.DB.ValidateAndCreate(i)
	ms.NoError(err)
	ms.False(verrs.HasAny())

	count, err := ms.DB.Count("invites")
	ms.NoError(err)
	ms.Equal(1, count)

}
