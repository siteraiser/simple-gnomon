package rpc

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/deroproject/derohe/walletapi/xswd"
	"github.com/gorilla/websocket"
)

var Conn *websocket.Conn

func Set_ws_conn() {
	websocket_endpoint := "ws://127.0.0.1:44326/xswd"
	var err error

	fmt.Printf("Connecting to %s\n", websocket_endpoint)

	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // allow self-signed certs
	}

	Conn, _, err = dialer.Dial(websocket_endpoint, nil)
	if err != nil {
		panic(err)
	}
	fmt.Println("WebSocket connected")
	appData := xswd.ApplicationData{}
	signature := `-----BEGIN DERO SIGNED MESSAGE-----
Address: dero1qyvqpdftj8r6005xs20rnflakmwa5pdxg9vcjzdcuywq2t8skqhvwqglt6x0g
C: 93ad8099661f34a162274368c8eb3ed99ff2bdacdeab8f4800a75a69fa6c935
S: 29bf7da6a5ef7c3d23f79acc37bf9c149321d693ada98837a4e761494f4adcff

MTcyNzJhYjk0NjJkMjViMzBhMWMwMjRiNWNiZjdjYjI3YjMyYzI5M2RjNjRhZDJi
YTM5MmYyOTk4ODIwZGVlZg==
-----END DERO SIGNED MESSAGE-----`
	appData.Signature = []byte(signature)
	appData.Name = "simple-gnomon-lite"
	appData.Description = "Creating application data must be simple and fun! :)"
	appData.Url = "http://localhost:8080"
	appData.Permissions = map[string]xswd.Permission{
		"SignData": xswd.AlwaysAllow, // because that's what this does
	}
	appData.Id = "17272ab9462d25b30a1c024b5cbf7cb27b32c293dc64ad2ba392f2998820deef"
	if err := Conn.WriteJSON(appData); err != nil {
		panic(err)
	}
	fmt.Println("Auth handshake sent")

	_, msg, err := Conn.ReadMessage()
	if err != nil {
		panic(err)
	}

	var res xswd.AuthorizationResponse
	if err := json.Unmarshal(msg, &res); err != nil {
		panic(err)
	}
	if !res.Accepted {
		panic(errors.New("app not accepted"))
	}
}

func postBytes(b []byte) []byte {

	err := Conn.WriteMessage(websocket.TextMessage, b)
	if err != nil {
		panic(err)
	}

	_, msg, err := Conn.ReadMessage()
	if err != nil {
		panic(err)
	}
	return msg
}

func signData(input string) []byte {

	// fmt.Println(estimate)
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      "SignData",
		"method":  "SignData",
		"params":  []byte(input),
	}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil
	}

	var r xswd.RPCResponse
	if err := json.Unmarshal(postBytes(jsonBytes), &r); err != nil {
		return nil
	}

	bytes, err := base64.StdEncoding.DecodeString(r.Result.(map[string]any)["signature"].(string))
	if err != nil {
		return nil
	}

	return bytes

}
