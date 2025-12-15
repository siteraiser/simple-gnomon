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

func main() {
	fmt.Println("starting ....")
	var err error
	db_name := fmt.Sprintf("sql%s.db", "GNOMON")
	wd := globals.GetDataDirectory()
	db_path := filepath.Join(wd, "gnomondb")
	if UseMem {
		sqlite, err = NewSqlDB(db_path, db_name)
	} else {
		sqlite, err = NewDiskDB(db_path, db_name)
		CreateTables(sqlite.DB)
	}

	if err != nil {
		fmt.Println("[Main] Err creating sqlite:", err)
		return
	}
	start_gnomon_indexer()
}

var TargetHeight = int64(0)
var HighestKnownHeight = api.Get_TopoHeight()
var sqlite = &SqlStore{}
var sqlindexer = &Indexer{}
var UseMem = false

func start_gnomon_indexer() {

	var lowest_height int64
	//Ask()
	height, err := sqlite.GetLastIndexHeight()
	if err != nil {
		height = startat
		fmt.Println("err: ", err)
	}
	lowest_height = height
	if UseMem == false {
		sqlite.PruneHeight(int(height))
	}

	sqlindexer = NewSQLIndexer(sqlite, height, []string{MAINNET_GNOMON_SCID})
	fmt.Println("SqlIndexer ", sqlindexer)
	fmt.Println("topoheight ", api.Get_TopoHeight())
	fmt.Println("lowest_height ", fmt.Sprint(lowest_height))
	//start := time.Now()

	if TargetHeight < HighestKnownHeight-25000 && lowest_height+25000 < HighestKnownHeight {
		TargetHeight = lowest_height + 25000
	} else {
		TargetHeight = HighestKnownHeight
	}

	var wg sync.WaitGroup
	for bheight := lowest_height; bheight < TargetHeight; bheight++ {
		if !api.Status_ok {
			break
		}

		api.Ask()
		//---- MAIN PRINTOUT
		showBlockStatus(bheight)
		wg.Add(1)
		go ProcessBlock(&wg, bheight)

	}
	// Wait for all requests to finish
	fmt.Println("indexed")
	wg.Wait()

	//Take a breather
	w, _ := time.ParseDuration("1s")
	time.Sleep(w)

	//check if there was a missing request
	if !api.Status_ok { //Start over from last saved.
		// Extract filename
		filename := filepath.Base(sqlite.DBPath)
		dir := filepath.Dir(sqlite.DBPath)
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

		api.Status_ok = true
		start_gnomon_indexer() //without saving
		return
	}

	//Essentials...
	last := HighestKnownHeight
	HighestKnownHeight = api.Get_TopoHeight()

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
		filename := filepath.Base(sqlite.DBPath)
		dir := filepath.Dir(sqlite.DBPath)
		// Start disk mode
		sqlite, err = NewDiskDB(dir, filename)
	}

	fmt.Println("Saving phase over...............................................................")
	sqlite.ViewTables()

	start_gnomon_indexer()

}

func ProcessBlock(wg *sync.WaitGroup, bheight int64) {
	defer wg.Done()
	if !api.Status_ok {
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

	batch_size := 4
	//Find total number of batches
	batch_count := int(math.Ceil(float64(tx_count) / float64(batch_size)))
	//Make an array to hold the result sets
	type mockRequest struct {
		Txs_as_hex []string
		Txs        []rpc.Tx_Related_Info
	}
	var r mockRequest
	//Go through the array of batches and collect the results
	for i := range batch_count {
		//var transaction_result rpc.GetTransaction_Result
		end := batch_size * i
		if i == batch_count-1 {
			end = len(tx_str_list)
		}
		api.Ask()

		tx := api.GetTransaction(rpc.GetTransaction_Params{
			Tx_Hashes: tx_str_list[batch_size*i : end],
		})
		r.Txs = append(r.Txs, tx.Txs...)
		r.Txs_as_hex = append(r.Txs_as_hex, tx.Txs_as_hex...)
	}

	//let the rest go unsaved if one request fails
	if !api.Status_ok {
		return
	}

	//likely an error
	if len(r.Txs_as_hex) == 0 {
		//	fmt.Println("-------r.Txs_as_hex", transaction_result)
		return
	}

	for i, tx_hex := range r.Txs_as_hex {
		Ask()
		wg2.Add(1)
		go saveDetails(&wg2, tx_hex, r.Txs[i].Signer, bheight)
	}

	wg2.Wait()
	if api.Status_ok {
		storeHeight(bheight)
	}

}
func storeHeight(bheight int64) {
	Ask()
	//--maybe replace by using add owner and add a height to there...
	if ok, err := sqlindexer.SSSBackend.StoreLastIndexHeight(int64(bheight)); !ok && err != nil {
		fmt.Println("Error Saving LastIndexHeight: ", err)
		if strings.Contains(err.Error(), "database is locked") {
			api.Status_ok = false
			//panic(err)
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
	//fmt.Println("\nReq: ", Processing-int64(bheight))

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
	// range the indexers and add to index 1 at a time to prevent out of memory error
	for key, name := range indexes {
		//fmt.Println("name: ", name)
		// if the code does not contain the filter, skip
		//probably could use some suring up here
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
	if err := sqlindexer.AddSCIDToIndex(staged); err != nil {
		fmt.Println(err, " ", staged.Scid, " ", staged.Fsi.Height)
		return
	}

}

/********************************/
/*********** Helpers ************/
/********************************/
var LastTime = time.Now()
var PriorTimes []int64

func getSpeed() int {
	t := time.Now()

	if len(PriorTimes) > 1000 {
		PriorTimes = PriorTimes[1000:]
	}
	PriorTimes = append(PriorTimes, time.Since(LastTime).Milliseconds())
	total := int64(0)
	for _, ti := range PriorTimes {
		total += ti
	}

	LastTime = t
	value := int64(0)
	if len(PriorTimes) != 0 {
		value = int64(total) / int64(len(PriorTimes))
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
		" Max En Route:" + strconv.Itoa(int(api.Max_preferred_requests)) +
		" Actual En Route:" + strconv.Itoa(int(api.Out1+api.Out2)) +
		" Speed:" + speedms + "ms" +
		" " + speedbph + "bph"

	fmt.Print("\r", show)
}
