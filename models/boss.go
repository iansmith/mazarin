package models

import (
	"encoding/json"
	"time"

	"github.com/gobuffalo/pop"
	"github.com/gobuffalo/uuid"
	"github.com/gobuffalo/validate"
	"github.com/gobuffalo/validate/validators"
)

// Boss maps a user to the account they can manage.  Single user can be the
// boss of zero or more accounts.
type Boss struct {
	ID        uuid.UUID `json:"id" db:"id"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
	Account   *Account  `belongs_to:"account"`
	AccountID uuid.UUID `json:"account_id" db:"account_id"`
	User      *User     `belongs_to:"user"`
	UserID    uuid.UUID `json:"user_id" db:"user_id"`
}

// String is not required by pop and may be deleted
func (b Boss) String() string {
	jb, _ := json.Marshal(b)
	return string(jb)
}

// Bosses is not required by pop and may be deleted
type Bosses []Boss

// String is not required by pop and may be deleted
func (b Bosses) String() string {
	jb, _ := json.Marshal(b)
	return string(jb)
}

// Validate gets run every time you call a "pop.Validate*" (pop.ValidateAndSave, pop.ValidateAndCreate, pop.ValidateAndUpdate) method.
// This method is not required and may be deleted.
func (b *Boss) Validate(tx *pop.Connection) (*validate.Errors, error) {
	return validate.Validate(
		&validators.UUIDIsPresent{Field: b.AccountID, Name: "AccountID"},
		&validators.UUIDIsPresent{Field: b.UserID, Name: "UserID"},
	), nil
}

// ValidateCreate gets run every time you call "pop.ValidateAndCreate" method.
// This method is not required and may be deleted.
func (b *Boss) ValidateCreate(tx *pop.Connection) (*validate.Errors, error) {
	return validate.NewErrors(), nil
}

// ValidateUpdate gets run every time you call "pop.ValidateAndUpdate" method.
// This method is not required and may be deleted.
func (b *Boss) ValidateUpdate(tx *pop.Connection) (*validate.Errors, error) {
	return validate.NewErrors(), nil
}
