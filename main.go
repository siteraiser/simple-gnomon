package main

import (
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
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
var blockBatchSize = int64(50000) // Batch size (how many to process before saving w/ mem mode)
var UseMem = true                 // Use in-memory db
var SpamLevel = 50

// Optimized settings for mode db mode
var memBatchSize = int16(100)
var memPreferredRequests = uint8(16)
var diskBatchSize = int16(100)
var diskPreferredRequests = uint8(16)

// Program vars
var TargetHeight = int64(0)
var HighestKnownHeight = int64(0)
var sqlite = &SqlStore{}
var sqlindexer = &Indexer{}
var batchSize = int16(0)
var firstRun = true

var RamSizeMB = int(0)

// Gnomon Index SCID
const MAINNET_GNOMON_SCID = "a05395bb0cf77adc850928b0db00eb5ca7a9ccbafd9a38d021c8d299ad5ce1a4"
const TESTNET_GNOMON_SCID = "c9d23d2fc3aaa8e54e238a2218c0e5176a6e48780920fd8474fac5b0576110a2"
const MAINNET_NAME_SERVICE_SCID = "0000000000000000000000000000000000000000000000000000000000000001"

// Hardcoded Smart Contracts of DERO Network
// TODO: Possibly in future we can pull this from derohe codebase
var Hardcoded_SCIDS = []string{MAINNET_NAME_SERVICE_SCID, MAINNET_GNOMON_SCID}

type action struct {
	Type  string
	Act   string
	Block int64
}

var CustomActions = map[string]action{}

func main() {
	var err error
	var text string
	fmt.Print("Enter system memory to use in GB(8,16,...): ")
	_, err = fmt.Scanln(&text)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	RamSizeMB, _ = strconv.Atoi(text)
	RamSizeMB *= int(1000)
	fmt.Println("SC spam threshold 50-100 recommended")
	fmt.Print("Enter number of name registrations allowed per wallet: ")
	_, err = fmt.Scanln(&text)
	SpamLevel := text
	fmt.Println("Set to ", SpamLevel)

	//Add custom actions for scids
	//	CustomActions[Hardcoded_SCIDS[0]] = action{Type: "SC", Act: "discard-before", Block: 161296} //saveasinteraction
	CustomActions[Hardcoded_SCIDS[0]] = action{Type: "SC", Act: "discard"}

	fmt.Println("starting ....")
	api.AssignConnections(false)
	HighestKnownHeight = api.GetTopoHeight()
	if HighestKnownHeight < 1 {
		fmt.Println("Error getting height ....", HighestKnownHeight)
	}

	db_name := fmt.Sprintf("sql%s.db", "GNOMON")
	wd := globals.GetDataDirectory()

	db_path := filepath.Join(wd, "gnomondb")
	if UseMem {
		filesize := int(fileSizeMB(filepath.Join(db_path, db_name)))
		filetoobig := RamSizeMB <= filesize
		if !filetoobig {
			fmt.Println("loading db into memory ....")
			batchSize = memBatchSize
			api.PreferredRequests = memPreferredRequests
			sqlite, err = NewSqlDB(db_path, db_name)
		}

		if filetoobig {
			fmt.Println("Switching to disk mode ....")
			UseMem = false
		}
	}
	if !UseMem { //|| memModeSelect(false)
		fmt.Println("loading db ....")
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
	var starting_height int64
	starting_height, err := sqlite.GetLastIndexHeight()
	if err != nil {
		if startAt == 0 {
			starting_height = findStart(0, HighestKnownHeight) //if it isn't set then find it
		}
		fmt.Println("err: ", err)
	}

	if firstRun == true || api.Status.ErrorCount != int64(0) {
		firstRun = false
		sqlite.TrimHeight(starting_height)
		api.Processing = api.Processing[0:0]
		if api.Status.ErrorCount != int64(0) {
			fmt.Println(strconv.Itoa(int(api.Status.ErrorCount))+" Error(s) detected! Type:", api.Status.ErrorType+" Name:"+api.Status.ErrorName+" Details:"+api.Status.ErrorDetail)
		}
	}
	//	if api.Status.ErrorCount != int64(0) || last_height % 1000 == 0 {
	api.AssignConnections(api.Status.ErrorCount != int64(0)) //might as well check/retry new connections here
	//		return
	//	}
	sqlindexer = NewSQLIndexer(sqlite, starting_height, CustomActions)

	fmt.Println("Topo Height ", api.GetTopoHeight())
	fmt.Println("last height ", fmt.Sprint(starting_height))

	if TargetHeight < HighestKnownHeight-blockBatchSize && starting_height+blockBatchSize < HighestKnownHeight {
		TargetHeight = starting_height + blockBatchSize
	} else {
		TargetHeight = HighestKnownHeight
	}

	var wg sync.WaitGroup
	for bheight := starting_height; bheight < TargetHeight; bheight++ {
		if !api.OK() {
			break
		}
		//---- MAIN PRINTOUT
		showBlockStatus(bheight)
		api.Ask()
		wg.Add(1)
		Mutex.Lock()
		api.Processing = append(api.Processing, bheight)
		Mutex.Unlock()
		go ProcessBlock(&wg, bheight)

	}
	// Wait for all requests to finish
	fmt.Println("Batch completed, count:", blockBatchSize)

	wg.Wait()

	//Take a breather
	w, _ := time.ParseDuration("1s")
	time.Sleep(w)

	//check if there was a missing request or a db error
	if !api.OK() { //Start over from last saved.
		start_gnomon_indexer() //without saving index height
		return
	}

	//Essentials...
	sqlite.StoreLastIndexHeight(TargetHeight)
	last := HighestKnownHeight
	HighestKnownHeight = api.GetTopoHeight()

	fmt.Println("last:", last)
	fmt.Println("TargetHeight:", TargetHeight)
	//maybe skip when caught up
	fmt.Println("Purging spam:", Spammers)
	sqlite.RidSpam()

	var switching = false
	if UseMem {
		fmt.Println("Saving Batch.............................................................")
		sqlite.BackupToDisk()
		//Check size
		if int64(RamSizeMB) <= fileSizeMB(sqlite.db_path) {
			switching = true
			fmt.Println("Switching to disk mode..............................", TargetHeight)
		}
	}

	if TargetHeight == last || switching {

		if !switching {
			fmt.Println("All caught up..............................", TargetHeight)
			t, _ := time.ParseDuration("5s")
			time.Sleep(t)
		}

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
		manageProcessing(bheight)
		return
	}

	result := api.GetBlockInfo(rpc.GetBlock_Params{
		Height: uint64(bheight),
	})

	//fmt.Println("result", result)
	bl := api.GetBlockDeserialized(result.Blob)

	if len(bl.Tx_hashes) < 1 {
		manageProcessing(bheight)
		return
	}
	var tx_str_list []string
	var regcount = 0
	for _, hash := range bl.Tx_hashes {
		if hash.String()[:5] == "00000" {
			regcount++
		}
		if hash.String() != "" {
			tx_str_list = append(tx_str_list, hash.String())
		}

	}

	tx_count := len(tx_str_list)

	if tx_count == 0 || regcount > 10 {
		manageProcessing(bheight)
		return
	}

	large := false
	if tx_count > 500 {
		large = true
		fmt.Println("LARGE BLOCK...")
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
		manageProcessing(bheight)
		return
	}

	//likely an error
	if len(r.Txs_as_hex) == 0 {
		//	fmt.Println("-------r.Txs_as_hex", transaction_result)
		manageProcessing(bheight)
		return
	}

	for i, tx_hex := range r.Txs_as_hex {
		wg2.Add(1)
		go saveDetails(&wg2, tx_hex, r.Txs[i].Signer, bheight, large)
	}

	wg2.Wait()
	if api.OK() {
		manageProcessing(bheight)
		return
	}

}

func storeHeight(bheight int64) {
	Ask()
	//--maybe replace by using add owner and add a height to there...
	if ok, err := sqlindexer.SSSBackend.StoreLastIndexHeight(int64(bheight)); !ok && err != nil {
		fmt.Println("Error Saving LastIndexHeight: ", err)
		if strings.Contains(err.Error(), "database is locked") {
			api.NewError("database", "db lock", "Storing last index")
		}
		return
	}
}

/********************************/
/********************************/
func saveDetails(wg2 *sync.WaitGroup, tx_hex string, signer string, bheight int64, large bool) {
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
		fmt.Println("\nTX Height: ", tx.Height)
		if strings.Contains(err.Error(), "Invalid Version in Transaction") {
			return
		}
		panic(err)
	}
	//

	if tx.TransactionType != transaction.SC_TX { //|| (len(tx.Payloads) > 10 && tx.Payloads[0].RPCType == byte(transaction.REGISTRATION))
		return
	}

	tx_type := ""
	fmt.Print("scid found at height:", fmt.Sprint(bheight)+"\n")
	params := rpc.GetSC_Params{}
	if tx.SCDATA.HasValue(rpc.SCCODE, rpc.DataString) {
		tx_type = "install"
		//	fmt.Println("install:", tx)

		params = rpc.GetSC_Params{
			SCID:       tx.GetHash().String(),
			Code:       true,
			Variables:  true,
			TopoHeight: bheight,
		}
	} else if tx.SCDATA.HasValue(rpc.SCID, rpc.DataHash) {
		tx_type = "invoke"
		//	fmt.Println("invoke:", tx)

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
			Variables:  CustomActions[tx.GetHash().String()].Act != "saveasinteraction", //no name spams
			TopoHeight: bheight,
		}
	}

	// Discard the discardable
	if CustomActions[params.SCID].Act == "discard" ||
		(CustomActions[params.SCID].Act == "discard-before" && CustomActions[params.SCID].Block >= bheight) {
		return
	}
	if (slices.Contains(Spammers, signer)) && params.SCID == Hardcoded_SCIDS[0] { //|| spammy == true
		return
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
	//panic(vars)
	//	fmt.Println("headers", headers)

	skipchecks := false
	if large == true && params.SCID == Hardcoded_SCIDS[0] {
		skipchecks = true
	}
	tags := ""
	class := ""
	if !skipchecks {
		for key, name := range indexes {
			for _, filter := range name {
				if !strings.Contains(sc.Code, filter) { //fmt.Sprintf("%.1000s",)
					continue
				}
				class = key
				tags = tags + "," + filter
			}
			if tags != "" && tags[0:1] == "," {
				tags = tags[1:]
			}

		}
	}
	entrypoint := ""
	if tx.SCDATA.HasValue("entrypoint", rpc.DataString) {
		entrypoint = tx.SCDATA.Value("entrypoint", rpc.DataString).(string)
	}

	staged := SCIDToIndexStage{
		Type:       tx_type,
		TXHash:     tx.GetHash().String(),
		Fsi:        &FastSyncImport{Height: uint64(bheight), Signer: signer, SCName: scname, SCDesc: scdesc, SCImgURL: scimgurl}, //
		ScVars:     vars,
		ScCode:     sc.Code,
		Params:     params,
		Entrypoint: entrypoint,
		Class:      class, //Class and tags are not in original gnomon
		Tags:       tags,
	}
	fmt.Println("staged scid:", staged.TXHash, ":", fmt.Sprint(staged.Fsi.Height))
	fmt.Println("staged params.scid:", params.SCID, ":", fmt.Sprint(staged.Fsi.Height))

	// now add the scid to the index
	Ask()
	// if the contract already exists, record the interaction
	ready(false)
	if err := sqlindexer.AddSCIDToIndex(staged); err != nil {
		fmt.Println(err, " ", staged.TXHash, " ", staged.Fsi.Height)
		if strings.Contains(err.Error(), "database is locked") {
			api.NewError("database", "db lock", "Adding index")
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

func manageProcessing(bheight int64) {
	i := -1
	api.Mutex.Lock()
	i = GetIndex(bheight)
	lastfirst := api.Processing[0]
	if i != -1 && i < len(api.Processing) {
		api.Processing = append(api.Processing[:i], api.Processing[i+1:]...)
	}
	tostore := int64(-1)
	if len(api.Processing) != 0 {
		tostore = api.Processing[0]
	}
	api.Mutex.Unlock()
	if lastfirst != tostore && tostore > 0 {
		storeHeight(tostore)
	}
}

func GetIndex(number int64) int {
	for i, v := range api.Processing {
		if v == number {
			return i
		}
	}
	return -1 // Not found
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
	outtot, text := getOutCounts()
	show := "Block:" + strconv.Itoa(int(bheight)) +
		" Connections " + strconv.Itoa(int(len(api.Outs))) +
		" " + text +
		" Speed:" + speedms + "ms" +
		" " + speedbph + "bph" +
		" Inserting " + strconv.Itoa(len(api.Processing)-outtot) +
		" Total Errors:" + strconv.Itoa(int(api.Status.TotalErrors))

	fmt.Print("\r", show)
}
func getOutCounts() (int, string) {
	text := ""
	spacer := ""
	tot := 0
	if api.PreferredRequests >= 10 {
		spacer = " "
	}
	for i, out := range api.Outs {
		insert := ""
		if int(api.Outs[i]) < 10 {
			insert = spacer
		}
		text += ":" + insert + strconv.Itoa(int(out))
		tot += int(out)
	}

	return tot, text[1:]
}

// Supply true to boot from disk, returns true if memory is nearly full
/*func memModeSelect(boot bool) bool {
	if mbFree() < 1000 {
		if boot {
			UseMem = false
			// Extract filename
			filename := filepath.Base(sqlite.db_path)
			dir := filepath.Dir(sqlite.db_path)
			// Start disk mode
			sqlite, _ = NewDiskDB(dir, filename)
		}
		return true
	}
	return false
}

// Supply true to boot from disk, returns true if memory is nearly full

func mbFree() int64 {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	return int64(memStats.HeapIdle-memStats.HeapSys) / 1024 / 1024
}
*/
func fileSizeMB(filePath string) int64 {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return 0
	}
	sizeBytes := fileInfo.Size()
	return int64(float64(sizeBytes) / (1024 * 1024))
}
