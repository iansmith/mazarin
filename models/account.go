package models

import (
	"encoding/json"
	"time"

	"github.com/gobuffalo/pop"
	"github.com/gobuffalo/uuid"
	"github.com/gobuffalo/validate"
	"github.com/gobuffalo/validate/validators"
)

// Account models some organization that pays me.
type Account struct {
	ID                  uuid.UUID `json:"id" db:"id"`
	CreatedAt           time.Time `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time `json:"updated_at" db:"updated_at"`
	Organization        string    `json:"organization" db:"organization"`
	Bosses              Bosses    `has_many:"bosses" order_by:"ID asc"`
	Users               Users     `has_many:"users" order_by:"slack_user_name asc"`
	Invites             Invites   `has_many:"invites" order_by:"ID asc"`
	CreatingTeam        string    `json:"creating_team" db:"creating_team"`
	CreatingChannel     string    `json:"creating_channel" db:"creating_channel"`
	CreatingChannelName string    `json:"creating_channel_name" db:"creating_channel_name"`
	Credits             float32   `json:"credits" db:"credits"`
}

// String is not required by pop and may be deleted
func (a Account) String() string {
	ja, _ := json.Marshal(a)
	return string(ja)
}

// Accounts is not required by pop and may be deleted
type Accounts []Account

// String is not required by pop and may be deleted
func (a Accounts) String() string {
	ja, _ := json.Marshal(a)
	return string(ja)
}

// Validate gets run every time you call a "pop.Validate*" (pop.ValidateAndSave, pop.ValidateAndCreate, pop.ValidateAndUpdate) method.
// This method is not required and may be deleted.
func (a *Account) Validate(tx *pop.Connection) (*validate.Errors, error) {
	return validate.Validate(
		&validators.StringIsPresent{Field: a.Organization, Name: "Organization"},
		&validators.StringIsPresent{Field: a.CreatingTeam, Name: "CreatingTeam"},
		&validators.StringIsPresent{Field: a.CreatingChannel, Name: "CreatingChannel"},
		&validators.StringIsPresent{Field: a.CreatingChannelName, Name: "CreatingChannelName"},
	), nil
}

// ValidateCreate gets run every time you call "pop.ValidateAndCreate" method.
// This method is not required and may be deleted.
func (a *Account) ValidateCreate(tx *pop.Connection) (*validate.Errors, error) {
	return validate.NewErrors(), nil
}

// ValidateUpdate gets run every time you call "pop.ValidateAndUpdate" method.
// This method is not required and may be deleted.
func (a *Account) ValidateUpdate(tx *pop.Connection) (*validate.Errors, error) {
	return validate.NewErrors(), nil
}
