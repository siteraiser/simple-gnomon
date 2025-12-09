package main

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/gorilla/websocket"
	"github.com/secretnamebasis/simple-gnomon/cmd"
	"github.com/secretnamebasis/simple-gnomon/connections"
	structures "github.com/secretnamebasis/simple-gnomon/structs"
)

func main() {

	a := app.NewWithID("simple-gnomon_" + rand.Text())
	w := a.NewWindow("simple-gnomon")
	w.Resize(fyne.NewSize(400, 200))
	endpoint := ""
	connection := widget.NewEntry()
	readout := widget.NewLabel("")
	indexed_height := widget.NewLabel("")
	current_height := widget.NewLabel("")
	blocks_per_hour := widget.NewLabel("")
	connection.SetPlaceHolder("127.0.0.1:10102")
	button := widget.NewButtonWithIcon("Start Gnomon Indexer", theme.MediaPlayIcon(), func() {
		// now go start gnomon
		endpoint = connection.Text
		os.Args = append(os.Args,
			"-endpoint="+endpoint,
			// the first g45 nft starts at 678864

			// "-progress",
		)
		go func() {
			// defer func() {
			// 	if r := recover(); r != nil {
			// 		// Handle/log the panic here
			// 		fyne.DoAndWait(func() { readout.SetText(fmt.Sprintf("gnomon failed: \n%v", r)) })
			// 	}
			// }()
			cmd.Start_gnomon_indexer()
		}()
		time.Sleep(time.Second * 5)
		go func() {
			var err error
			url := "ws://127.0.0.1:9190/ws"
			dialer := websocket.Dialer{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // allow self-signed certs
			}
			indexer_connection, _, err = dialer.Dial(url, nil)
			if err != nil {
				panic(err)
			}
			last := float64(0)

			for range time.NewTicker(time.Second).C {
				result, err := getAllSCIDSAndOwners(getParams{IDX: "all"})
				if err != nil {
					panic(err)
				}
				all := strconv.Itoa(len(result.Result))
				text := "ALL SCIDS & OWNERS: " + all + "\n"
				result, err = getAllSCIDSAndOwners(getParams{IDX: "g45"})
				if err != nil {
					panic(err)
				}
				g45 := strconv.Itoa(len(result.Result))
				text += "ALL G45 & OWNERS: " + g45 + "\n"
				result, err = getAllSCIDSAndOwners(getParams{IDX: "nfa"})
				if err != nil {
					panic(err)
				}
				nfa := strconv.Itoa(len(result.Result))
				text += "ALL NFAs & OWNERS: " + nfa
				height1, err := getLastIndexHeight(getParams{IDX: "all"})
				if err != nil {
					panic(err)
				}
				now := connections.GetDaemonInfo().TopoHeight
				if last == 0 {
					last = height1.Result
				}
				tick := height1.Result - last
				last = height1.Result
				fmt.Println(tick)
				tick *= 60 * 60
				fyne.DoAndWait(func() {
					readout.SetText(text)
					current_height.SetText("current height:" + strconv.Itoa(int(now)))
					indexed_height.SetText("indexed height:" + strconv.Itoa(int(height1.Result)))
					blocks_per_hour.SetText("blocks per hour:" + strconv.Itoa(int(tick)))
				})
			}

		}()
	})
	connection.OnSubmitted = func(s string) { button.OnTapped() }
	connection.ActionItem = button
	content := container.NewVBox(
		readout,
		current_height,
		indexed_height,
		blocks_per_hour,
		connection,
	)
	w.SetContent(content)
	w.ShowAndRun()
}

type getAllSCIDSAndOwnersResult struct {
	Result map[string]any `json:"result"`
}

type getParams struct {
	IDX string
}

var indexer_connection *websocket.Conn

func getAllSCIDSAndOwners(params getParams) (getAllSCIDSAndOwnersResult, error) {

	msg := map[string]any{
		"method": "GetAllOwnersAndSCIDs",
		"id":     "1",
		"params": params,
	}

	var err error

	if err := indexer_connection.WriteJSON(msg); err != nil {
		return getAllSCIDSAndOwnersResult{}, errors.New("failed to write")
	}

	_, b, err := indexer_connection.ReadMessage()
	if err != nil {
		return getAllSCIDSAndOwnersResult{}, errors.New("failed to read")
	}

	var r structures.JSONRpcResp
	if err := json.Unmarshal(b, &r); err != nil {
		return getAllSCIDSAndOwnersResult{}, errors.New("failed to unmarshal")
	}

	return getAllSCIDSAndOwnersResult{r.Result.(map[string]any)}, nil
}

type getLastHeightResult struct {
	Result float64 `json:"result"`
}

func getLastIndexHeight(params getParams) (getLastHeightResult, error) {

	msg := map[string]any{
		"method": "GetLastIndexHeight",
		"id":     "1",
		"params": params,
	}

	var err error

	if err := indexer_connection.WriteJSON(msg); err != nil {
		return getLastHeightResult{}, errors.New("failed to write")
	}

	_, b, err := indexer_connection.ReadMessage()
	if err != nil {
		return getLastHeightResult{}, errors.New("failed to read")
	}

	var r structures.JSONRpcResp
	if err := json.Unmarshal(b, &r); err != nil {
		return getLastHeightResult{}, errors.New("failed to unmarshal")
	}

	return getLastHeightResult{r.Result.(float64)}, nil
}
