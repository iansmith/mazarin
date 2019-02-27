package models

import (
	"encoding/json"
	"time"

	"github.com/gobuffalo/pop"
	"github.com/gobuffalo/uuid"
	"github.com/gobuffalo/validate"
	"github.com/gobuffalo/validate/validators"
)

const (
	// InviteLiveTime is how long an invite should be good for.
	InviteLiveTime = time.Hour * 72
)

// Invite is an time-limited ability to add a user to account.
type Invite struct {
	ID         uuid.UUID `json:"id" db:"id"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time `json:"updated_at" db:"updated_at"`
	ExpiresAt  time.Time `json:"expires_at" db:"expires_at"`
	InviteCode uuid.UUID `json:"invite_code" db:"invite_code"`
	Used       bool      `json:"used" db:"used"`
	User       User      `belongs_to:"user"`
	UserID     uuid.UUID `json:"user_id" db:"user_id"`
}

// String is not required by pop and may be deleted
func (i Invite) String() string {
	ji, _ := json.Marshal(i)
	return string(ji)
}

// Invites is not required by pop and may be deleted
type Invites []Invite

// String is not required by pop and may be deleted
func (i Invites) String() string {
	ji, _ := json.Marshal(i)
	return string(ji)
}

// Validate gets run every time you call a "pop.Validate*" (pop.ValidateAndSave, pop.ValidateAndCreate, pop.ValidateAndUpdate) method.
// This method is not required and may be deleted.
func (i *Invite) Validate(tx *pop.Connection) (*validate.Errors, error) {
	return validate.Validate(
		&validators.UUIDIsPresent{Field: i.InviteCode, Name: "InviteCode"},
		&validators.TimeIsPresent{Field: i.ExpiresAt, Name: "ExpiresAt"},
		MustBeBoolValue(i.Used, false, "Used"),
	), nil
}

// ValidateCreate gets run every time you call "pop.ValidateAndCreate" method.
// This method is not required and may be deleted.
func (i *Invite) ValidateCreate(tx *pop.Connection) (*validate.Errors, error) {
	return validate.NewErrors(), nil
}

// ValidateUpdate gets run every time you call "pop.ValidateAndUpdate" method.
// This method is not required and may be deleted.
func (i *Invite) ValidateUpdate(tx *pop.Connection) (*validate.Errors, error) {
	return validate.NewErrors(), nil
}
