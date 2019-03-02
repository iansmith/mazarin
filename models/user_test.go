package models

import (
	"github.com/gobuffalo/pop/nulls"
	"github.com/gobuffalo/uuid"
)

func (ms *ModelSuite) Test_UserCreate() {
	ms.LoadFixture("two simple accounts")

	var account Account
	err := ms.DB.Last(&account)
	if err != nil {
		ms.Fail("can't get last")
	}

	accountID := uuid.Must(uuid.NewV4())
	u1 := &User{
		SlackUserID:           nulls.NewString("user123"),
		SlackUserName:         nulls.NewString("joe shmoe"),
		TimezoneName:          nulls.NewString("timezone/New_York"),
		TimezoneOffsetMinutes: nulls.NewInt32(-300),
		Enabled:               true,
		AccountID:             account.ID,
	}
	verrs, err := ms.DB.ValidateAndSave(u1)
	ms.NoError(err)
	ms.False(verrs.HasAny())

	ms.False(u1.CreatedAt.IsZero())
	ms.False(u1.UpdatedAt.IsZero())

	u2 := &User{
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
	ms.Equal(4, count) //three in fixture, one created here

}
