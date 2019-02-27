package models

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/gobuffalo/pop"
	"github.com/gobuffalo/pop/nulls"
	"github.com/gobuffalo/uuid"
	"github.com/gobuffalo/validate"
	"github.com/gobuffalo/validate/validators"
)

//User models a user that likely was created via slack.
type User struct {
	ID                    uuid.UUID    `json:"id" db:"id"`
	CreatedAt             time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt             time.Time    `json:"updated_at" db:"updated_at"`
	SlackUserID           nulls.String `json:"slack_user_id" db:"slack_user_id"`
	SlackUserName         nulls.String `json:"slack_user_name" db:"slack_user_name"`
	TimezoneName          nulls.String `json:"timezone_name" db:"timezone_name"`
	TimezoneOffsetMinutes nulls.Int32  `json:"timezone_offset_minutes" db:"timezone_offset_minutes"`
	Location              nulls.String `json:"location" db:"location"`
	Enabled               bool         `json:"enabled" db:"enabled"`
	Account               *Account     `belongs_to:"account"`
	AccountID             uuid.UUID    `json:"account_id" db:"account_id"`
}

// String is not required by pop and may be deleted
func (u User) String() string {
	ju, _ := json.Marshal(u)
	return string(ju)
}

// Users is not required by pop and may be deleted
type Users []User

// String is not required by pop and may be deleted
func (u Users) String() string {
	ju, _ := json.Marshal(u)
	return string(ju)
}

// Validate gets run every time you call a "pop.Validate*" (pop.ValidateAndSave, pop.ValidateAndCreate, pop.ValidateAndUpdate) method.
// This method is not required and may be deleted.
func (u *User) Validate(tx *pop.Connection) (*validate.Errors, error) {
	return validate.Validate(
		StringPresent(u.SlackUserName, "SlackUserName"),
		StringPresent(u.SlackUserID, "SlackUserID"),
		&validators.UUIDIsPresent{Field: u.AccountID, Name: "AccountID"},
	), nil
}

// StringPresent is a workaround for the fact that StringIsPresent seems to
// not understand nulls.String.
func StringPresent(field nulls.String, name string) *validators.FuncValidator {
	return &validators.FuncValidator{
		Fn:      func() bool { return stringNeitherNilNorEmpty(field) },
		Field:   name,
		Name:    name,
		Message: "%s is either null or the empty string, value required",
	}
}
func stringNeitherNilNorEmpty(s nulls.String) bool {
	if s.Valid == false {
		return false
	}
	result := strings.TrimSpace(s.String)
	return result != ""
}

// MustBeBoolValue enforces that a field must have a particular boolean value.
func MustBeBoolValue(field bool, value bool, name string) *validators.FuncValidator {
	v := "true"
	if !value {
		v = "false"
	}
	return &validators.FuncValidator{
		Fn:      func() bool { return field == value },
		Field:   name,
		Name:    name,
		Message: "%s must be set to " + v,
	}
}

// ValidateCreate gets run every time you call "pop.ValidateAndCreate" method.
// This method is not required and may be deleted.
func (u *User) ValidateCreate(tx *pop.Connection) (*validate.Errors, error) {
	return validate.Validate(
		MustBeBoolValue(u.Enabled, true, "Enabled"),
	), nil
}

// ValidateUpdate gets run every time you call "pop.ValidateAndUpdate" method.
// This method is not required and may be deleted.
func (u *User) ValidateUpdate(tx *pop.Connection) (*validate.Errors, error) {
	return validate.NewErrors(), nil
}
