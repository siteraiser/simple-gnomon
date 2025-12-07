package main

import (
	"errors"
	"flag"
	"fmt"

	"github.com/secretnamebasis/simple-gnomon/connections"
	"github.com/ybbus/jsonrpc"
)

var endpoint = flag.String("endpoint", "", "-endpoint=<DAEMON_IP:PORT>")
var starting_height = flag.Int64("starting_height", -1, "-starting_height=123")
var ending_height = flag.Int64("ending_height", -1, "-ending_height=123")
var help = flag.Bool("help", false, "-help")
var established_backup bool
var achieved_current_height int64
var lowest_height int64
var day_of_blocks int64

func main() {
	flag.Parse()
	if help != nil && *help {
		fmt.Println(`Usage: simple-gnomon [options]
A simple indexer for the DERO blockchain. 

Options:
  -endpoint <DAEMON_IP:PORT>   Address of the daemon to connect to.
  -starting_height <N>                Height to pop to the back to.
  -help                        Show this help message.`)
		return
	}

	if endpoint != nil && *endpoint == "" {

		// first call on the wallet ws for authorizations
		connections.Set_ws_conn()

		// next, establish the daemon endpoint for rpc calls, waaaaay faster than through the wallet
		daemon := connections.GetDaemonEndpoint()
		*endpoint = daemon.Endpoint
	}

	connections.RpcClient = jsonrpc.NewClient("http://" + *endpoint + "/json_rpc")

	// if you are getting a zero... yeah, you are not connected
	if connections.Get_TopoHeight() == 0 {
		panic(errors.New("please connect through rpc"))
	}

	day_of_blocks = ((60 * 60 * 24) / int64(connections.GetDaemonInfo().Target))

	// now go start gnomon
	start_gnomon_indexer()

}
