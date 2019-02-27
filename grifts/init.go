package grifts

import (
	"github.com/gobuffalo/buffalo"
	"github.com/iansmith/mazarin/actions"
)

func init() {
	buffalo.Grifts(actions.App())
}
