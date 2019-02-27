package models_test

import (
	"github.com/gofrs/uuid"
	"github.com/iansmith/mazarin/models"
)

func (ms *ModelSuite) Test_BossCreate() {
	id := uuid.Must(uuid.NewV4())
	accountID := uuid.Must(uuid.NewV4())
	userID := uuid.Must(uuid.NewV4())
	b1 := &models.Boss{
		ID:        id,
		AccountID: accountID,
		UserID:    userID,
	}
	verrs, err := ms.DB.ValidateAndCreate(b1)
	ms.NoError(err)
	ms.False(verrs.HasAny())

	count, err := ms.DB.Count("bosses")
	ms.NoError(err)
	ms.Equal(1, count)

}
