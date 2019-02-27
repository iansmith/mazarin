package models_test

import (
	"fmt"

	"github.com/gobuffalo/uuid"
	"github.com/iansmith/mazarin/models"
)

func (ms *ModelSuite) Test_AccountCreate() {

	count, err := ms.DB.Count("accounts")
	ms.NoError(err)
	ms.Equal(0, count)

	acct := &models.Account{
		Organization:        "my org",
		CreatingTeam:        "my team",
		CreatingChannel:     "mychan",
		CreatingChannelName: "she watch channel zero",
		Credits:             0.0,
	}

	verrs, err := ms.DB.ValidateAndCreate(acct)
	ms.NoError(err)
	fmt.Printf("XXXXXX %+v\n", verrs)
	ms.False(verrs.HasAny())
	ms.False(acct.CreatedAt.IsZero())
	ms.False(acct.UpdatedAt.IsZero())

	acct = &models.Account{
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
