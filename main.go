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
	"sync/atomic"
	"time"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/globals"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
	api "github.com/secretnamebasis/simple-gnomon/models"
)

var startAt = int64(0) // Start at Block Height, will be auto-set when using 0
var blockBatchSize int64
var blockBatchSizeMem = int64(25000)
var blockBatchSizeDisk = int64(5000) // Batch size (how many to process before saving w/ mem mode)
var UseMem = true                    // Use in-memory db
var SpamLevel = 50

// Optimized settings for mode db mode
var memBatchSize = int16(100)
var memPreferredRequests = uint8(16)
var diskBatchSize = int16(64)
var diskPreferredRequests = uint8(10)

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

var rcount int32
var rlimit = int32(2000)

func main() {
	var err error
	var text string
	fmt.Print("Enter system memory to use in GB(0,2,8,...): ")
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
	CustomActions[Hardcoded_SCIDS[1]] = action{Type: "SC", Act: "discard"}
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
			blockBatchSize = blockBatchSizeMem
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
		blockBatchSize = blockBatchSizeDisk
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

	//Errors
	if firstRun == true || api.Status.ErrorCount != int64(0) {
		firstRun = false
		sqlite.TrimHeight(starting_height)
		api.Batches = []api.Batch{}
		api.TXIDSProcessing = []string{}
		api.BatchCount = 0
		if api.Status.ErrorCount != int64(0) {
			fmt.Println(strconv.Itoa(int(api.Status.ErrorCount))+" Error(s) detected! Type:", api.Status.ErrorType+" Name:"+api.Status.ErrorName+" Details:"+api.Status.ErrorDetail)
		}
	}
	api.Blocks = []api.Block{} //clear here

	api.AssignConnections(api.Status.ErrorCount != int64(0)) //might as well check/retry new connections here
	api.StartingFrom = int(starting_height)

	sqlindexer = NewSQLIndexer(sqlite, starting_height, CustomActions)

	fmt.Println("Topo Height ", HighestKnownHeight)
	fmt.Println("Last Height ", fmt.Sprint(starting_height))

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
		api.Ask("height")
		wg.Add(1)
		api.Mutex.Lock()
		api.Blocks = append(api.Blocks, api.Block{Height: bheight})
		api.Mutex.Unlock()
		go ProcessBlock(&wg, bheight)
		checkGo()
	}

	wg.Wait()

	//check if there was a missing request or a db error
	if !api.OK() { //Start over from last saved.
		start_gnomon_indexer() //without saving index height
		return
	}
	// Wait for all requests to finish
	fmt.Println("Batch completing, count:", blockBatchSize)

	place := 0
	count := 0
	for {
		loading := []string{" .. .", ". .. ", ".. .."}
		fmt.Print("\r", loading[place])

		place++
		if place == 3 {
			place = 0
		}
		count++
		//Mutex.Lock()
		if (api.BatchCount == 0 && len(api.TXIDSProcessing) == 0) || count > 600 || !api.OK() { // wait for 10 mins
			break
		}
		if len(api.TXIDSProcessing) != 0 {
			fmt.Println(api.TXIDSProcessing)
		}
		w, _ := time.ParseDuration("1s")
		time.Sleep(w)
	}

	if count <= 600 {
		sqlite.StoreLastIndexHeight(TargetHeight)
	}

	last := HighestKnownHeight
	HighestKnownHeight = api.GetTopoHeight()
	if HighestKnownHeight < 1 {
		api.AssignConnections(true)
		fmt.Println("Error getting height ....", HighestKnownHeight)
		HighestKnownHeight = api.GetTopoHeight()
		if HighestKnownHeight < 1 {
			panic("Too many failed connections")
		}
	}

	fmt.Println("Last:", last)
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
		blockBatchSize = blockBatchSizeDisk
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
	discarding := false

	if !api.OK() {
		return
	}

	result := api.GetBlockInfo(rpc.GetBlock_Params{
		Height: uint64(bheight),
	})
	bl := api.GetBlockDeserialized(result.Blob)

	if len(bl.Tx_hashes) < 1 {
		discarding = true
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
		discarding = true
	}
	//good place to set large block flag if needed

	api.Mutex.Lock()
	if !discarding {
		api.BlockByHeight(bheight).TxIds = append(api.BlockByHeight(bheight).TxIds, tx_str_list...)
		api.TXIDSProcessing = append(api.TXIDSProcessing, tx_str_list...)
	} else {
		api.RemoveBlocks(int(bheight))
	}
	txidlen := len(api.TXIDSProcessing)
	if txidlen >= 100 || len(api.Batches) == 0 {
		var wga sync.WaitGroup
		//Find total number of batches
		batch_count := int(math.Ceil(float64(txidlen) / float64(batchSize)))
		api.Mutex.Unlock()
		//Go through the array of batches and collect the results
		for i := range batch_count {
			end := int(batchSize) * i
			api.Mutex.Lock()
			if txidlen >= 100 && i == batch_count-1 {
				api.Mutex.Unlock()
				continue
			}
			start := int(batchSize) * i
			if i == batch_count-1 {
				end = txidlen
			}
			txs := api.TXIDSProcessing[start:end]
			api.TXIDSProcessing = api.TXIDSProcessing[end:]
			api.Mutex.Unlock()
			atomic.AddInt32(&rcount, 1)
			checkGo()
			wga.Add(1)
			go DoBatch(&wga, api.Batch{TxIds: txs})
		}
		wga.Wait()
	} else {
		api.Mutex.Unlock()
	}
}

var laststored = int64(0)

func DoBatch(wga *sync.WaitGroup, batch api.Batch) {
	defer wga.Done()
	defer atomic.AddInt32(&rcount, -1)
	api.Mutex.Lock()

	api.BatchCount++
	api.Mutex.Unlock()
	var wg2 sync.WaitGroup
	var r rpc.GetTransaction_Result
	api.Ask("tx")
	r = api.GetTransaction(rpc.GetTransaction_Params{
		Tx_Hashes: batch.TxIds, //[int(batchSize)*i : end]
	})
	showBlockStatus(-1)
	if !api.OK() {
		return
	}
	//var tx transaction.Transaction
	for i, tx_hex := range r.Txs_as_hex {
		tx, err := decodeTx(tx_hex)
		if err == nil {
			wg2.Add(1)
			go saveDetails(&wg2, tx, r.Txs[i].Block_Height, r.Txs[i].Signer, batch)
		} else {
			remove := []string{r.Txs[i].Tx_hash}
			if tx.SCDATA.HasValue(rpc.SCCODE, rpc.DataString) {
				remove = append(remove, tx.GetHash().String())
			} else if tx.SCDATA.HasValue(rpc.SCID, rpc.DataHash) {
				_, ok := tx.SCDATA.Value(rpc.SCID, rpc.DataHash).(crypto.Hash)
				if ok {
					remove = append(remove, tx.GetHash().String())
				}
			}
			api.RemoveTXs(remove)
			updateBlocks(api.Batch{
				TxIds: remove,
			})
		}
	}
	wg2.Wait()

	if api.OK() {
		api.Mutex.Lock()
		api.BatchCount--
		api.RemoveTXs(batch.TxIds)
		api.Mutex.Unlock()
		updateBlocks(batch)
	}
}
func saveDetails(wg2 *sync.WaitGroup, tx transaction.Transaction, bheight int64, signer string, batch api.Batch) { //, large bool
	defer wg2.Done()
	var wg3 sync.WaitGroup
	var ok = true
	api.RemoveTXs([]string{tx.GetHash().String()})

	if tx.TransactionType != transaction.SC_TX { //|| (len(tx.Payloads) > 10 && tx.Payloads[0].RPCType == byte(transaction.REGISTRATION))
		ok = false
	}
	tx_type := ""
	//fmt.Print("scid found at height:", fmt.Sprint(bheight)+"\n")
	params := rpc.GetSC_Params{}
	if tx.SCDATA.HasValue(rpc.SCCODE, rpc.DataString) {
		tx_type = "install"
		fmt.Println("\nSC Code:\n", tx.SCDATA.Value(rpc.SCCODE, rpc.DataString))

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
		if !ok || scid.String() == "" {
			ok = false
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
		ok = false
	}
	if (slices.Contains(Spammers, signer)) && params.SCID == Hardcoded_SCIDS[0] { //|| spammy == true
		ok = false
	}
	if ok {
		wg3.Add(1)
		go processSCs(&wg3, tx, tx_type, params, bheight, signer)
		wg3.Wait()
	}
	updateBlocks(api.Batch{
		TxIds: []string{tx.GetHash().String()},
	})
}

func processSCs(wg3 *sync.WaitGroup, tx transaction.Transaction, tx_type string, params rpc.GetSC_Params, bheight int64, signer string) {
	defer wg3.Done()

	api.Ask("sc")
	sc := api.GetSC(params) //Variables: true,

	vars, err := GetSCVariables(sc.VariableStringKeys, sc.VariableUint64Keys)
	if err != nil { //might be worth investigating what errors could occur
		return
	}

	kv := sc.VariableStringKeys

	//fmt.Println("key", kv)
	scname := api.GetSCNameFromVars(kv)
	scdesc := ""
	scimgurl := ""
	tags := ""
	class := ""
	if params.SCID != Hardcoded_SCIDS[0] { //only need the name for these
		scdesc = api.GetSCDescriptionFromVars(kv)
		scimgurl = api.GetSCIDImageURLFromVars(kv)
		for key, name := range map[string][]string{
			"g45":   {"G45-AT", "G45-C", "G45-FAT", "G45-NAME", "T345"},
			"nfa":   {"ART-NFA-MS1"},
			"swaps": {"StartSwap"},
			"tela":  {"docVersion", "telaVersion"},
		} {
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

	//fmt.Println("staged scid:", staged.TXHash, ":", fmt.Sprint(staged.Fsi.Height))
	//fmt.Println("staged params.scid:", params.SCID, ":", fmt.Sprint(staged.Fsi.Height))
	showBlockStatus(-1)
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

func storeHeight(bheight int64) {
	Ask()
	//fmt.Println("Saving LastIndexHeight: ", bheight)
	if ok, err := sqlindexer.SSSBackend.StoreLastIndexHeight(int64(bheight)); !ok && err != nil {
		fmt.Println("Error Saving LastIndexHeight: ", err)
		if strings.Contains(err.Error(), "database is locked") {
			api.NewError("database", "db lock", "Storing last index")
		}
		return
	}
}

func decodeTx(tx_hex string) (transaction.Transaction, error) {
	b, err := hex.DecodeString(tx_hex)
	if err != nil {
		panic(err)
	}
	var tx transaction.Transaction
	if err := tx.Deserialize(b); err != nil {
		fmt.Println("\nTX Height: ", tx.Height)
		if strings.Contains(err.Error(), "Invalid Version in Transaction") ||
			strings.Contains(err.Error(), "Transaction version unknown") {
			return tx, err
		}
		panic(err)
	}
	return tx, err
}
func updateBlocks(batch api.Batch) {
	api.Mutex.Lock()
	var remove = []int64{}
	for _, block := range api.Blocks {
		if block.Processed || block.Height == 0 {
			remove = append(remove, block.Height)
		}
	}
	for height := range remove {
		api.RemoveBlocks(int(height))
	}
	api.Mutex.Unlock()
	for _, block := range api.Blocks {
		if block.Height > laststored {
			storeHeight(block.Height)
			laststored = block.Height
			break
		}
	}
}

/********************************/
/********************************/
/*
fmt.Println("batch.TxIds:", len(batch.TxIds))

	fmt.Println("api.BlockByHeight(bheight).TxIds:", len(api.BlockByHeight(bheight).TxIds))
	for _, t := range batch.TxIds {

		if slices.Contains(api.BlockByHeight(bheight).TxIds, t) {
			fmt.Println(bheight, " (bheight).TxIds Contains:", t)
		}
		if !slices.Contains(api.BlockByHeight(bheight).TxIds, t) {
			fmt.Println(bheight, " (bheight).TxIds NOT Contains:", t)
		}

		api.ProcessBlocks(t) //api.RemoveTXs(batch.TxIds)
}
	if len(tx.Txs_as_hex) != len(batch.TxIds) {
		fmt.Println(len(r.Txs_as_hex), " fffffff ", len(batch.TxIds))
		for i, _ := range r.Txs_as_hex {
			fmt.Println(int64(r.Txs[i].Block_Height), " - ", r.Txs[i])
		}
		panic(r)
	}

*/
/********************************/
/*********** Helpers ************/
/********************************/
func checkGo() {
	for {
		current := atomic.LoadInt32(&rcount)
		if current > rlimit {
			time.Sleep(1 * time.Millisecond)
		} else {
			break
		}
	}
}
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

var status = struct {
	block int64
}{
	block: 0,
}

func showBlockStatus(bheight int64) {
	if bheight != -1 {
		status.block = bheight
	}
	speedms := "0"
	speedbph := "0"
	s := getSpeed()
	if s != 0 {
		speedms = strconv.Itoa(s)
		speedbph = strconv.Itoa((1000 / s) * 60 * 60)
	}
	_, text := getOutCounts()
	show := "Block:" + strconv.Itoa(int(status.block)) +
		" Connections " + strconv.Itoa(int(len(api.Outs))) +
		" " + text +
		" Speed:" + speedms + "ms" +
		" " + speedbph + "bph" +
		" Processing " + strconv.Itoa(len(api.Blocks)) +
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
