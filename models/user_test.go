package models_test

import (
	"github.com/gobuffalo/pop/nulls"
	"github.com/gobuffalo/uuid"
	"github.com/iansmith/mazarin/models"
)

func (ms *ModelSuite) Test_UserCreate() {
	accountID := uuid.Must(uuid.NewV4())
	u1 := &models.User{
		SlackUserID:           nulls.NewString("user123"),
		SlackUserName:         nulls.NewString("joe shmoe"),
		TimezoneName:          nulls.NewString("timezone/New_York"),
		TimezoneOffsetMinutes: nulls.NewInt32(-300),
		Enabled:               true,
		AccountID:             accountID,
	}
	verrs, err := ms.DB.ValidateAndSave(u1)
	ms.NoError(err)
	ms.False(verrs.HasAny())

	ms.False(u1.CreatedAt.IsZero())
	ms.False(u1.UpdatedAt.IsZero())

	u2 := &models.User{
		SlackUserID:           nulls.NewString("slackuser"),
		SlackUserName:         nulls.NewString("slackname"),
		TimezoneName:          nulls.NewString("timezone/New_York"),
		TimezoneOffsetMinutes: nulls.NewInt32(-300),
		Enabled:               false, /* can't create account in this state*/
		AccountID:             accountID,
	}
	verrs, err = ms.DB.ValidateAndCreate(u2)
	ms.NoError(err)
	ms.True(verrs.HasAny())
	errs := verrs.Get("enabled")
	ms.Len(errs, 1)

	count, err := ms.DB.Count("users")
	ms.NoError(err)
	ms.Equal(1, count)

}
