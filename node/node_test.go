package node

import (
	"encoding/hex"
	"log"
	"testing"

	"github.com/tokentransfer/demo/core"
	"github.com/tokentransfer/demo/util"

	"github.com/tokentransfer/chain/block"

	. "gopkg.in/check.v1"
)

type NodeSuite struct{}

func Test_Node(t *testing.T) {
	s := Suite(&NodeSuite{})
	TestingRun(t, s)
	// TestingT(t)
}

func (suite *NodeSuite) TestProcess(c *C) {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	config, err := core.NewConfig("./config.json")
	c.Assert(err, IsNil)

	n := NewNode()
	err = n.Init(config)
	c.Assert(err, IsNil)

	n.load()

	blob := "65080112146DA68A0C5DAAE0715AE6B62F00F548A2C6981C2F1801280A321442F32B004DA1093D51AE40A58F38E33BA4F46397424104D33A199D322FAFD28867E3B9FB2F5AC081D56CFF1AE803635730E1D01B77D837D9EE578346DD88B68D21B9A61B8F1EFE9B2574F08B4A471F864FA7EA7A29185C4A9B0136373438333733333630343737333338313637303835393437343231303038393430343332333336323633393934393931323730323937363539343531383135353733303035393339333737353A3936383434383933313339383031373734373539303232373632353437303332363832393333343331343935323532353133353935393832313331383331303735333536393836373934383930"
	data, err := hex.DecodeString(blob)
	c.Assert(err, IsNil)

	tx := &block.Transaction{}
	err = tx.UnmarshalBinary(data)
	c.Assert(err, IsNil)
	txWithData, _, err := n.processTransaction(tx)
	c.Assert(err, IsNil)

	util.PrintJSON(">>> tx", tx)
	util.PrintJSON(">>> txWithData", txWithData)
}
