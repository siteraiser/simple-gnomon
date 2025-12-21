package rpc

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/deroproject/derohe/block"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/walletapi"
	"github.com/ybbus/jsonrpc"
)

var Mutex sync.Mutex

// No Errors
func OK() bool {
	if Status.DbOk && Status.ApiOk {
		return true
	}
	return false
}

// Error type, name, details
func NewError(einfo ...string) {
	Status.Mutex.Lock()

	switch einfo[0] {
	case "database":
		Status.DbOk = false
	case "connection", "rpc":
		Status.ApiOk = false
	}
	Status.ErrorCount++
	Status.ErrorType = einfo[0]
	Status.ErrorName = einfo[1]
	if len(einfo) == 3 {
		Status.ErrorDetail = einfo[2]
	}

	Status.Mutex.Unlock()
}

// Reset Errors
func Reset() {
	Status.TotalErrors += Status.ErrorCount
	Status.ErrorCount = 0
	Status.ErrorType = ""
	Status.ErrorName = ""
	Status.ErrorDetail = ""
	Status.DbOk = true
	Status.ApiOk = true
}

type State struct {
	ErrorType   string
	ErrorName   string
	ErrorDetail string
	DbOk        bool
	ApiOk       bool
	ErrorCount  int64
	TotalErrors int64
	OK          interface {
		OK() bool
	}
	Reset interface {
		Reset()
	}
	Errors interface {
		NewError(etype string, ename string)
	}
	sync.Mutex
}

var Status = &State{
	ErrorType:   "",
	ErrorName:   "",
	ErrorDetail: "",
	DbOk:        true,
	ApiOk:       true,
	ErrorCount:  0,
	TotalErrors: 0,
}

var Endpoints = [3]string{"64.226.81.37:10102", "node.derofoundation.org:11012", "dero-node-ch4k1pu.mysrv.cloud"} //"64.226.81.37:10102"
var currentEndpoint = Endpoints[0]
var Processing []int64

func Ask() {

	for {
		time.Sleep(time.Microsecond)
		if len(Processing) > 1000 {
			time.Sleep(time.Millisecond)
			if len(Processing) > 5000 {
				time.Sleep(time.Millisecond * 100)
			}
		}
		Mutex.Lock()
		noofouts := int16(len(EndpointAssignments))
		maxr := PreferredRequests * noofouts
		tot := int16(0)
		for _, out := range Outs {
			tot += out
		}
		if tot < maxr {
			ready := checkOuts()
			if ready != -1 {
				currentEndpoint = Endpoints[ready]
				Mutex.Unlock()
				return
			}
		}
		Mutex.Unlock()
	}
}

var Outs []int16

var EndpointAssignments = make(map[string]int16)

func checkOuts() int {

	lowest := int16(100)
	lowest_id := 0
	for _, id := range EndpointAssignments {
		if Outs[id] < lowest {
			lowest_id = int(id)
		}
	}

	if Outs[lowest_id] < PreferredRequests {
		return lowest_id
	}

	return -1
}

var PreferredRequests = int16(0)

func AssignConnections(iserror bool) {
	params := rpc.GetInfo_Params{}
	if iserror {
		Outs = Outs[0:0]
		EndpointAssignments = make(map[string]int16)
	}

	for i, endpoint := range Endpoints {
		if _, ok := EndpointAssignments[endpoint]; ok {
			continue
		}
		var result any
		var rpcClient jsonrpc.RPCClient
		nodeaddr := "http://" + endpoint + "/json_rpc"
		rpcClient = jsonrpc.NewClient(nodeaddr)
		err := rpcClient.CallFor(&result, "DERO.GetInfo", params) // no params argument
		if err != nil {
			EndpointAssignments[endpoint] = int16(i)
			Outs = append(Outs, 0)
		}
	}
	Reset()
}

func callRPC[t any](method string, params any, validator func(t) bool) t {

	result, err := getResult[t](method, params)

	if err != nil {
		//	log.Fatal(err)
		var zero t
		return zero
	}

	if !validator(result) {
		fmt.Println(errors.New("failed validation"), method)
		var zero t
		return zero
	}

	return result
}

func getResult[T any](method string, params any) (T, error) {
	var result T
	var err error
	var rpcClient jsonrpc.RPCClient
	var endpoint string

	Mutex.Lock()

	endpoint = currentEndpoint
	nodeaddr := "http://" + endpoint + "/json_rpc"
	rpcClient = jsonrpc.NewClient(nodeaddr)

	Outs[EndpointAssignments[endpoint]]++
	Mutex.Unlock()

	if params == nil {
		err = rpcClient.CallFor(&result, method) // no params argument
	} else {
		err = rpcClient.CallFor(&result, method, params)
	}

	Mutex.Lock()

	Outs[EndpointAssignments[endpoint]]--
	Mutex.Unlock()

	if err != nil {

		if strings.Contains(err.Error(), "-32098") && strings.Contains(err.Error(), "mismatch") { //Tx statement roothash mismatch ref blid... skip it
			fmt.Println(err)

			var zero T
			return zero, err
		} else if strings.Contains(err.Error(), "-32098") && strings.Contains(err.Error(), "many parameters") { //Using batching now so this shouldn't occur
			fmt.Println(err)
			log.Fatal("Daemon is not compatible (" + nodeaddr + ")")
		} else if strings.Contains(err.Error(), "wsarecv: A connection attempt failed("+nodeaddr+")") {
			//maybe handle connection errors here with a cancel / rollback instead.
			NewError("connection", method, "")
			fmt.Println(err)
			//	log.Fatal(err)
		} else {
			fmt.Println(endpoint)
			NewError("rpc", method, endpoint)
		}
	}

	return result, nil
}
func GetTopoHeight() int64 {
	validator := func(r rpc.GetInfo_Result) bool {
		return r.TopoHeight != 0
	}
	result := callRPC("DERO.GetInfo", nil, validator)
	return result.TopoHeight
}

func GetTransaction(params rpc.GetTransaction_Params) rpc.GetTransaction_Result {
	validator := func(r rpc.GetTransaction_Result) bool {

		return r.Status != ""
	}
	result := callRPC("DERO.GetTransaction", params, validator)
	return result
}

func GetBlockInfo(params rpc.GetBlock_Params) rpc.GetBlock_Result {
	validator := func(r rpc.GetBlock_Result) bool {
		return r.Block_Header.Depth != 0 //false //
	}
	result := callRPC("DERO.GetBlock", params, validator)
	return result
}

func GetTxPool() rpc.GetTxPool_Result {
	validator := func(r rpc.GetTxPool_Result) bool {
		return r.Status != ""
	}
	result := callRPC("DERO.GetTxPool", nil, validator)
	return result
}

func GetDaemonInfo() rpc.GetInfo_Result {
	validator := func(r rpc.GetInfo_Result) bool {
		return r.TopoHeight != 0
	}
	result := callRPC("DERO.GetInfo", nil, validator)
	return result
}

func GetSC(scParam rpc.GetSC_Params) rpc.GetSC_Result {
	validator := func(r rpc.GetSC_Result) bool {
		if scParam.Code {
			return r.Code != ""
		}
		return true
	}
	result := callRPC("DERO.GetSC", scParam, validator)
	return result
}

func GetSCCode(scid string) rpc.GetSC_Result {
	return GetSC(rpc.GetSC_Params{
		SCID:       scid,
		Code:       true,
		Variables:  false,
		TopoHeight: walletapi.Get_Daemon_Height(),
	})
}

func GetSCValues(scid string) rpc.GetSC_Result {
	return GetSC(rpc.GetSC_Params{
		SCID:       scid,
		Code:       false,
		Variables:  true,
		TopoHeight: walletapi.Get_Daemon_Height(),
	})
}

func GetSCIDImage(keys map[string]interface{}) image.Image {
	// simple way to set timeouts
	const timeout = time.Second * 9 // the world is a really big place
	for k, v := range keys {
		if !strings.Contains(k, "image") && !strings.Contains(k, "icon") {
			continue
		}
		encoded := v.(string)
		b, e := hex.DecodeString(encoded)
		if e != nil {
			fmt.Println(e, encoded)
			continue
		}
		value := string(b)
		fmt.Println("scid", "key", k, "value", value)

		//	furi, err := storage.ParseURI(value)
		//	fmt.Println("storage.ParseURI:", furi)

		uri, err := url.Parse(value) //storage.ParseURI(value)
		fmt.Println("url.Parse:", uri)
		if err != nil {
			fmt.Println(err, value)
			return nil
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, "GET", uri.String(), nil)
			if err != nil {
				fmt.Println(err, "get error")
				return nil
			}
			client := http.DefaultClient
			resp, err := client.Do(req)
			if err != nil || resp.StatusCode != http.StatusOK {
				return nil
			} else {
				defer resp.Body.Close()
				i, _, err := image.Decode(resp.Body)
				if err != nil {
					return nil
				}
				return i
			}
		}
	}
	return nil
}
func GetSCNameFromVars(keys map[string]interface{}) string {
	var text string

	for k, v := range keys {
		if !strings.Contains(k, "name") {
			continue
		}

		var e error
		var b []byte
		switch v := v.(type) {
		case string:
			b, e = hex.DecodeString(v)
			if e != nil {
				continue
			}
		default:
			continue // what else can we do ?
		}

		text = string(b)
		fmt.Println("Name found:", text)
	}
	if text == "" {
		return ""
	}
	return text
}
func GetSCDescriptionFromVars(keys map[string]interface{}) string {
	var text string

	for k, v := range keys {
		if !strings.Contains(k, "descr") {
			continue
		}
		b, e := hex.DecodeString(v.(string))
		if e != nil {
			continue // what else can we do ?
		}
		text = string(b)
	}
	if text == "" {
		return ""
	}
	return text
}

func GetSCIDImageURLFromVars(keys map[string]interface{}) string {
	var text string

	for k, v := range keys {
		if !strings.Contains(k, "imageurl") {
			continue
		}
		b, e := hex.DecodeString(v.(string))
		if e != nil {
			continue // what else can we do ?
		}
		text = string(b)
	}
	if text == "" {
		return ""
	}
	return text
}
func GetBlockDeserialized(blob string) block.Block {

	var bl block.Block
	b, err := hex.DecodeString(blob)
	if err != nil {
		// should probably log or handle this error
		fmt.Println(err.Error())
		return block.Block{}
	}
	bl.Deserialize(b)
	return bl
}
