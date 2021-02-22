package core

import (
	"testing"

	. "gopkg.in/check.v1"
)

type ConfigSuite struct{}

func Test_Config(t *testing.T) {
	s := Suite(&ConfigSuite{})
	TestingRun(t, s)
	// TestingT(t)
}

func (suite *ConfigSuite) TestConfig(c *C) {
	config, err := NewConfig("../config.json")
	c.Assert(err, IsNil)
	c.Assert(config, NotNil)
}
