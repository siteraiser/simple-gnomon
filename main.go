package main

import (
	"encoding/hex"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/globals"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
	api "github.com/secretnamebasis/simple-gnomon/models"
)

var startAt = int64(0)            // Start at Block Height, will be auto-set when using 0
var blockBatchSize = int64(25000) // Batch size (how many to process before saving w/ mem mode)
var UseMem = true                 // Use in-memory db
// Optimized settings for mode db mode
var memBatchSize = int16(8)
var memPreferredRequests = int16(10)
var diskBatchSize = int16(4)
var diskPreferredRequests = int16(8)

// Program vars
var TargetHeight = int64(0)
var HighestKnownHeight = api.GetTopoHeight()
var sqlite = &SqlStore{}
var sqlindexer = &Indexer{}
var batchSize = int16(0)
var firstRun = true

// Gnomon Index SCID
const MAINNET_GNOMON_SCID = "a05395bb0cf77adc850928b0db00eb5ca7a9ccbafd9a38d021c8d299ad5ce1a4"
const TESTNET_GNOMON_SCID = "c9d23d2fc3aaa8e54e238a2218c0e5176a6e48780920fd8474fac5b0576110a2"

// Hardcoded Smart Contracts of DERO Network
// TODO: Possibly in future we can pull this from derohe codebase
var Hardcoded_SCIDS = []string{"0000000000000000000000000000000000000000000000000000000000000001"}

func main() {

	fmt.Println("starting ....")
	var err error
	db_name := fmt.Sprintf("sql%s.db", "GNOMON")
	wd := globals.GetDataDirectory()
	db_path := filepath.Join(wd, "gnomondb")
	if UseMem {
		batchSize = memBatchSize
		api.PreferredRequests = memPreferredRequests
		sqlite, err = NewSqlDB(db_path, db_name)
	} else {
		batchSize = diskBatchSize
		api.PreferredRequests = diskPreferredRequests
		sqlite, err = NewDiskDB(db_path, db_name)
		CreateTables(sqlite.DB)
	}

	if err != nil {
		fmt.Println("[Main] Err creating sqlite:", err)
		return
	}
	start_gnomon_indexer()
}

func start_gnomon_indexer() {

	var last_height int64
	last_height, err := sqlite.GetLastIndexHeight()
	if err != nil {
		if startAt == 0 {
			last_height = findStart(1, HighestKnownHeight) //if it isn't set then find it
		}
		fmt.Println("err: ", err)
	}

	if firstRun == true || api.Status.ErrorCount != int64(0) {
		firstRun = false
		sqlite.PruneHeight(int(last_height))
		if api.Status.ErrorCount != int64(0) {
			fmt.Println("Index re-coordinated. Error Type:", api.Status.ErrorType+" Error Name:"+api.Status.ErrorName)
		}
		api.Reset()
	}

	sqlindexer = NewSQLIndexer(sqlite, last_height, []string{MAINNET_GNOMON_SCID})

	fmt.Println("Topo Height ", api.GetTopoHeight())
	fmt.Println("last height ", fmt.Sprint(last_height))

	if TargetHeight < HighestKnownHeight-blockBatchSize && last_height+blockBatchSize < HighestKnownHeight {
		TargetHeight = last_height + blockBatchSize
	} else {
		TargetHeight = HighestKnownHeight
	}

	var wg sync.WaitGroup
	for bheight := last_height; bheight < TargetHeight; bheight++ {
		if !api.OK() {
			break
		}
		//---- MAIN PRINTOUT
		showBlockStatus(bheight)
		api.Ask()
		wg.Add(1)
		go ProcessBlock(&wg, bheight)

	}
	// Wait for all requests to finish
	fmt.Println("indexed")
	wg.Wait()

	//Take a breather
	w, _ := time.ParseDuration("1s")
	time.Sleep(w)

	//check if there was a missing request or a db error
	if !api.OK() { //Start over from last saved.
		start_gnomon_indexer() //without saving index height
		return
	}
	/*
	   //check if there was a missing request
	   	if !api.StatusOk { //Start over from last saved.
	   		// Extract filename
	   		filename := filepath.Base(sqlite.db_path)
	   		dir := filepath.Dir(sqlite.db_path)
	   		//start from last saved to disk to ensure integrity (play it safe for now)
	   		if UseMem {
	   			sqlite, err = NewSqlDB(dir, filename)
	   		} else {
	   			sqlite, err = NewDiskDB(dir, filename)
	   		}

	   		if err != nil {
	   			fmt.Println("[Main] Err creating sqlite:", err)
	   			return
	   		}

	   		api.StatusOk = true
	   		start_gnomon_indexer() //without saving
	   		return
	   	}
	*/
	//Essentials...
	last := HighestKnownHeight
	HighestKnownHeight = api.GetTopoHeight()

	fmt.Println("last:", last)
	fmt.Println("TargetHeight:", TargetHeight)

	if UseMem {
		fmt.Println("Saving Batch.............................................................")
		sqlite.StoreLastIndexHeight(TargetHeight)
		sqlite.BackupToDisk()
	}
	if TargetHeight == last {
		if UseMem == false {
			fmt.Println("Saving after batch")
			sqlite.StoreLastIndexHeight(TargetHeight)
		}

		fmt.Println("All caught up..............................", TargetHeight)
		t, _ := time.ParseDuration("5s")
		time.Sleep(t)
		UseMem = false

		// Extract filename
		filename := filepath.Base(sqlite.db_path)
		dir := filepath.Dir(sqlite.db_path)
		// Start disk mode
		sqlite, err = NewDiskDB(dir, filename)
	}

	fmt.Println("Saving phase over...............................................................")
	sqlite.ViewTables()

	start_gnomon_indexer()

}

func ProcessBlock(wg *sync.WaitGroup, bheight int64) {
	defer wg.Done()
	if !api.OK() {
		return
	}

	result := api.GetBlockInfo(rpc.GetBlock_Params{
		Height: uint64(bheight),
	})

	//fmt.Println("result", result)
	bl := api.GetBlockDeserialized(result.Blob)

	if len(bl.Tx_hashes) < 1 {
		return
	}
	var tx_str_list []string
	for _, hash := range bl.Tx_hashes {
		tx_str_list = append(tx_str_list, hash.String())
	}

	tx_count := len(tx_str_list)

	if tx_count == 0 {
		return
	}
	var wg2 sync.WaitGroup

	//Find total number of batches
	batch_count := int(math.Ceil(float64(tx_count) / float64(batchSize)))
	//Make an array to hold the result sets
	type mockRequest struct {
		Txs_as_hex []string
		Txs        []rpc.Tx_Related_Info
	}
	var r mockRequest
	//Go through the array of batches and collect the results
	for i := range batch_count {
		end := int(batchSize) * i
		if i == batch_count-1 {
			end = len(tx_str_list)
		}
		api.Ask()
		tx := api.GetTransaction(rpc.GetTransaction_Params{
			Tx_Hashes: tx_str_list[int(batchSize)*i : end],
		})
		r.Txs = append(r.Txs, tx.Txs...)
		r.Txs_as_hex = append(r.Txs_as_hex, tx.Txs_as_hex...)
	}

	//let the rest go unsaved if one request fails
	if !api.OK() {
		return
	}

	//likely an error
	if len(r.Txs_as_hex) == 0 {
		//	fmt.Println("-------r.Txs_as_hex", transaction_result)
		return
	}

	for i, tx_hex := range r.Txs_as_hex {
		wg2.Add(1)
		go saveDetails(&wg2, tx_hex, r.Txs[i].Signer, bheight)
	}

	wg2.Wait()
	if api.OK() {
		storeHeight(bheight)
	}

}
func storeHeight(bheight int64) {
	Ask()
	//--maybe replace by using add owner and add a height to there...
	if ok, err := sqlindexer.SSSBackend.StoreLastIndexHeight(int64(bheight)); !ok && err != nil {
		fmt.Println("Error Saving LastIndexHeight: ", err)
		if strings.Contains(err.Error(), "database is locked") {
			api.NewError("database", "db lock")
		}
		return
	}
}

/********************************/
/********************************/
func saveDetails(wg2 *sync.WaitGroup, tx_hex string, signer string, bheight int64) {
	defer wg2.Done()

	indexes := map[string][]string{
		"g45":   {"G45-AT", "G45-C", "G45-FAT", "G45-NAME", "T345"},
		"nfa":   {"ART-NFA-MS1"},
		"swaps": {"StartSwap"},
		"tela":  {"docVersion", "telaVersion"},
	}

	b, err := hex.DecodeString(tx_hex)
	if err != nil {
		panic(err)
	}

	var tx transaction.Transaction
	if err := tx.Deserialize(b); err != nil {
		panic(err)
	}
	//fmt.Println("\nTX Height: ", tx.Height)

	if tx.TransactionType != transaction.SC_TX {
		return
	}

	fmt.Print("scid found at height:", fmt.Sprint(bheight)+"\n")
	params := rpc.GetSC_Params{}
	if tx.SCDATA.HasValue(rpc.SCCODE, rpc.DataString) {

		params = rpc.GetSC_Params{
			SCID:       tx.GetHash().String(),
			Code:       true,
			Variables:  true,
			TopoHeight: bheight,
		}
	}

	if tx.SCDATA.HasValue(rpc.SCID, rpc.DataHash) {
		scid, ok := tx.SCDATA.Value(rpc.SCID, rpc.DataHash).(crypto.Hash)

		if !ok { // paranoia
			return
		}
		if scid.String() == "" { // yeah... weird
			return
		}

		params = rpc.GetSC_Params{
			SCID:       scid.String(),
			Code:       false,
			Variables:  false,
			TopoHeight: bheight,
		}
	}

	api.Ask()
	sc := api.GetSC(params) //Variables: true,

	vars, err := GetSCVariables(sc.VariableStringKeys, sc.VariableUint64Keys)
	if err != nil { //might be worth investigating what errors could occur
		return
	}

	kv := sc.VariableStringKeys
	//fmt.Println("key", kv)
	scname := api.GetSCNameFromVars(kv)
	scdesc := api.GetSCDescriptionFromVars(kv)
	scimgurl := api.GetSCIDImageURLFromVars(kv)

	//	fmt.Println("headers", headers)
	tags := ""
	class := ""

	for key, name := range indexes {
		for _, filter := range name {
			if !strings.Contains(sc.Code, filter) {
				continue
			}
			class = key
			tags = tags + "," + filter
		}
		if tags != "" && tags[0:1] == "," {
			tags = tags[1:]
		}

	}
	staged := SCIDToIndexStage{
		Scid:   tx.GetHash().String(),
		Fsi:    &FastSyncImport{Height: uint64(bheight), Owner: signer, SCName: scname, SCDesc: scdesc, SCImgURL: scimgurl}, //
		ScVars: vars,
		ScCode: sc.Code,
		Class:  class, //Class and tags are not in original gnomon
		Tags:   tags,
	}
	fmt.Println("staged scid:", staged.Scid, ":", fmt.Sprint(staged.Fsi.Height))
	fmt.Println("staged params.scid:", params.SCID, ":", fmt.Sprint(staged.Fsi.Height))

	// now add the scid to the index
	Ask()
	// if the contract already exists, record the interaction
	ready(false)
	if err := sqlindexer.AddSCIDToIndex(staged); err != nil {
		fmt.Println(err, " ", staged.Scid, " ", staged.Fsi.Height)
		if strings.Contains(err.Error(), "database is locked") {
			api.NewError("database", "db lock")
		}
	}
	ready(true)

}

/********************************/
/*********** Helpers ************/
/********************************/

func findStart(start int64, top int64) (block int64) {

	difference := top - start
	offset := difference / 2

	if top-start == 1 {
		return top - 1
	}
	if api.GetBlockInfo(rpc.GetBlock_Params{Height: uint64(block)}).Status == "OK" {
		return findStart(start, offset+start)
	} else {
		return findStart(offset+start, top)
	}

}

var lastTime = time.Now()
var priorTimes []int64

func getSpeed() int {
	t := time.Now()

	if len(priorTimes) > 1000 {
		priorTimes = priorTimes[1000:]
	}
	priorTimes = append(priorTimes, time.Since(lastTime).Milliseconds())
	total := int64(0)
	for _, ti := range priorTimes {
		total += ti
	}

	lastTime = t
	value := int64(0)
	if len(priorTimes) != 0 {
		value = int64(total) / int64(len(priorTimes))
	}
	return int(value)
}

func showBlockStatus(bheight int64) {
	speedms := "0"
	speedbph := "0"
	s := getSpeed()
	if s != 0 {
		speedms = strconv.Itoa(s)
		speedbph = strconv.Itoa((1000 / s) * 60 * 60)
	}
	show := "Block:" + strconv.Itoa(int(bheight)) +
		" Max En Route:" + strconv.Itoa(int(api.PreferredRequests)) +
		" En Route " + strconv.Itoa(int(api.Out1)) +
		":" + strconv.Itoa(int(api.Out2)) +
		" Speed:" + speedms + "ms" +
		" " + speedbph + "bph" +
		" Total Errors:" + strconv.Itoa(int(api.Status.TotalErrors))

	fmt.Print("\r", show)
}
