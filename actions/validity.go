package actions

import (
	"github.com/gobuffalo/pop"
	"github.com/iansmith/mazarin/models"
)

// HasPaid checks to see if the user's account has positive
// credits.
func HasPaid(slackID string) (bool, error) {
	user := models.User{}
	pop.Debug = true

	err := models.DB.Transaction(func(tx *pop.Connection) error {
		err := tx.Eager().Where("slack_user_id = ?", slackID).First(&user)
		if err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		return false, err
	}
	return user.Account.Credits > 0.0, nil
}
