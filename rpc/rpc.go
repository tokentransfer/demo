package rpc

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/tokentransfer/demo/core"
	"github.com/tokentransfer/demo/node"
	"github.com/tokentransfer/demo/util"
	libcore "github.com/tokentransfer/interfaces/core"
)

type RPCService struct {
	config *core.Config
	node   *node.Node
}

func NewRPCService(n *node.Node) *RPCService {
	rpc := &RPCService{
		node: n,
	}
	return rpc
}

func (service *RPCService) Init(c libcore.Config) error {
	service.config = c.(*core.Config)
	return nil
}

func IndexHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "welcome to rpc service.")
}

func wrapResult(id interface{}, result interface{}, err error) interface{} {
	ret := map[string]interface{}{
		"id":      id,
		"jsonrpc": "2.0",
	}
	if err != nil {
		ret["error"] = err.Error()
	} else {
		ret["result"] = result
	}
	return ret
}

func writeResult(w http.ResponseWriter, id interface{}, result interface{}, err error) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(wrapResult(id, result, err)); err != nil {
		log.Println(err)
	}
}

func (service *RPCService) rpcService(w http.ResponseWriter, r *http.Request) {
	m := &map[string]interface{}{}
	decoder := json.NewDecoder(r.Body)

	if err := decoder.Decode(m); err != nil {
		writeResult(w, 0, nil, err)
	} else {
		params := util.ToArray(m, "params")
		id := util.ToUint64(m, "id")
		method := util.ToString(m, "method")

		fmt.Println("rpc", r.RequestURI, method, len(params))
		result, err := service.node.Call(method, params)
		if err != nil {
			writeResult(w, id, nil, err)
		} else {
			writeResult(w, id, result, nil)
		}
		fmt.Println("response", err)
	}
}

func (service *RPCService) Start() error {
	rpcAddress := service.config.GetRPCAddress()
	rpcPort := service.config.GetRPCPort()

	address := fmt.Sprintf("%s:%d", rpcAddress, rpcPort)

	http.HandleFunc("/", IndexHandler)
	http.HandleFunc("/jsonrpc", service.rpcService)
	fmt.Println("rpc server started on", address)
	http.ListenAndServe(address, nil)
	return nil
}
