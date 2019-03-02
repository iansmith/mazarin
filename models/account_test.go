package models

import (
	"github.com/gobuffalo/uuid"
)

func (ms *ModelSuite) Test_AccountCreate() {

	count, err := ms.DB.Count("accounts")
	ms.NoError(err)
	ms.Equal(0, count)

	acct := &Account{
		Organization:        "my org",
		CreatingTeam:        "my team",
		CreatingChannel:     "mychan",
		CreatingChannelName: "she watch channel zero",
		Credits:             0.0,
	}

	verrs, err := ms.DB.ValidateAndCreate(acct)
	ms.NoError(err)
	ms.False(verrs.HasAny())
	ms.False(acct.CreatedAt.IsZero())
	ms.False(acct.UpdatedAt.IsZero())

	acct = &Account{
		ID:                  uuid.Must(uuid.NewV4()),
		Organization:        "",
		CreatingTeam:        "my team",
		CreatingChannel:     "mychan",
		CreatingChannelName: "she watch channel zero",
		Credits:             0.0,
	}
	verrs, err = ms.DB.ValidateAndCreate(acct)
	ms.NoError(err)
	ms.True(verrs.HasAny())
	errs := verrs.Get("organization")
	ms.Len(errs, 1)

	count, err = ms.DB.Count("accounts")
	ms.NoError(err)
	ms.Equal(1, count)

}
