package main

import (
	"flag"
	"log"

	"github.com/tokentransfer/demo/core"
	"github.com/tokentransfer/demo/node"
	"github.com/tokentransfer/demo/rpc"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	configFile := "./config.json"
	flag.StringVar(&configFile, "config", configFile, "config file")
	flag.Parse()

	config, err := core.NewConfig(configFile)
	if err != nil {
		panic(err)
	}

	n := node.NewNode()
	err = n.Init(config)
	if err != nil {
		panic(err)
	}
	err = n.Start()
	if err != nil {
		panic(err)
	}

	r := rpc.NewRPCService(n)
	err = r.Init(config)
	if err != nil {
		panic(err)
	}
	go r.Start()

	n.StartServer()
}
