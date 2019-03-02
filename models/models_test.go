package models

import (
	"testing"

	"github.com/gobuffalo/packr"
	"github.com/gobuffalo/suite"
)

type ModelSuite struct {
	*suite.Model
}

func Test_ModelSuite(t *testing.T) {
	model, err := suite.NewModelWithFixtures(packr.NewBox("../fixtures"))
	if err != nil {
		t.Fatal(err)
	}

	ms := &ModelSuite{
		Model: model,
	}
	suite.Run(t, ms)
}
