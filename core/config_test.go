package core

import (
	"testing"

	. "github.com/tokentransfer/check"
)

type ConfigSuite struct{}

func Test_Config(t *testing.T) {
	s := Suite(&ConfigSuite{})
	TestingRun(t, s)
}

func (suite *ConfigSuite) TestConfig(c *C) {
	config, err := NewConfig("../config.json")
	c.Assert(err, IsNil)
	c.Assert(config, NotNil)
}
