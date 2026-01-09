package main

import (
	"encoding/hex"
	"flag"
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
	"github.com/secretnamebasis/simple-gnomon/api"
	"github.com/secretnamebasis/simple-gnomon/daemon"
	sql "github.com/secretnamebasis/simple-gnomon/db"
	"github.com/secretnamebasis/simple-gnomon/show"
	"github.com/secretnamebasis/simple-gnomon/structs"
)

var Mutex sync.Mutex
var startAt = int64(0) // Start at Block Height, will be auto-set when using 0
var blockBatchSize int64
var blockBatchSizeMem = int64(10000)
var blockBatchSizeDisk = int64(5000) // Batch size (how many to process before saving w/ mem mode)
var UseMem = true                    // Use in-memory db
//var SpamLevel = 50

// Optimized settings for mode db mode
var memBatchSize = int16(100)
var memPreferredRequests = int8(10)
var diskBatchSize = int16(64)
var diskPreferredRequests = int8(10)

// Program vars
var TargetHeight = int64(0)
var HighestKnownHeight = int64(0)
var sqlite = &sql.SqlStore{}
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

var Filters = map[string][]string{
	"g45":   {"G45-AT", "G45-C", "G45-FAT", "G45-NAME", "T345"},
	"nfa":   {"ART-NFA-MS1"},
	"swaps": {"StartSwap"},
	"tela":  {"docVersion", "telaVersion"},
}

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

	fmt.Println("SC spam threshold 0-50 recommended")
	fmt.Print("Enter number of name registrations allowed per wallet: ")
	_, err = fmt.Scanln(&text)
	SpamLevel := text

	fmt.Println("Use smoothing? 0-1000")
	_, err = fmt.Scanln(&text)
	daemon.Smoothing, _ = strconv.Atoi(text)
	fmt.Println("Smoothing period: ", daemon.Smoothing)

	fmt.Println("Choose display mode, 0, 1 or 2")
	_, err = fmt.Scanln(&text)
	show.DisplayMode, _ = strconv.Atoi(text)

	fmt.Println("Enter custom connection or enter n to use the default remote connections eg. node.derofoundation.org:10102")
	_, err = fmt.Scanln(&text)
	if text != "n" {
		daemon.Endpoints = []daemon.Connection{
			{Address: text},
		}
	}

	//if the port is set then launch the server
	portFlag := flag.Int("port", 0000, "string")
	flag.Parse()
	port := strconv.Itoa(*portFlag)
	if port != "0" {
		go api.Start(port)
	} else {
		//ask
		fmt.Println("Enter a port number for the api or n to skip")
		_, err = fmt.Scanln(&text)
		if _, err := strconv.Atoi(text); err == nil && text != "n" {
			go api.Start(text)
		}
	}

	fmt.Println("Start Gnomon indexer? y or n")
	_, err = fmt.Scanln(&text)
	if text == "n" {
		panic("Exited")
	}
	//Add custom actions for scids
	//CustomActions[Hardcoded_SCIDS[0]] = action{Type: "SC", Act: "discard-before", Block: 161296} //saveasinteraction
	if SpamLevel == "0" {
		CustomActions[Hardcoded_SCIDS[0]] = action{Type: "SC", Act: "discard"}
	}
	CustomActions[Hardcoded_SCIDS[1]] = action{Type: "SC", Act: "discard"}
	CustomActions["bb43c3eb626ee767c9f305772a6666f7c7300441a0ad8538a0799eb4f12ebcd2"] = action{Type: "SC", Act: "discard"}

	fmt.Println("Waking the GNOMON ...")

	HighestKnownHeight = daemon.GetTopoHeight()
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
			fmt.Println("Loading db into memory")
			batchSize = memBatchSize
			blockBatchSize = blockBatchSizeMem
			daemon.PreferredRequests = memPreferredRequests
			//Create the tables now...
			sqlite, err = sql.NewDiskDB(db_path, db_name)
			sql.CreateTables(sqlite.DB)
			sqlite.DB.Close()
			sqlite, err = sql.NewSqlDB(db_path, db_name)
		}

		if filetoobig {
			fmt.Println("Switching to disk mode ....")
			UseMem = false
		}
	}
	if !UseMem { //|| memModeSelect(false)
		fmt.Println("Loading db ....")
		batchSize = diskBatchSize
		blockBatchSize = blockBatchSizeDisk
		daemon.PreferredRequests = diskPreferredRequests
		sqlite, err = sql.NewDiskDB(db_path, db_name)
		sql.CreateTables(sqlite.DB)
	}

	if err != nil {
		fmt.Println("[Main] Err creating sqlite:", err)
		return
	}

	sql.StartAt = startAt
	sql.SpamLevel = SpamLevel
	show.PreferredRequests = &daemon.PreferredRequests
	show.Status = daemon.Status
	start_gnomon_indexer()
}

func start_gnomon_indexer() {
	var starting_height int64
	starting_height, err := sqlite.GetLastIndexHeight()
	if err != nil {
		if sql.StartAt == 0 {
			starting_height = findStart(0, HighestKnownHeight) //if it isn't set then find it
		}
		show.NewMessage(show.Message{Text: "err: ", Err: err})
	}

	//Errors
	if firstRun == true || daemon.Status.ErrorCount != int64(0) {
		firstRun = false
		daemon.TXIDSProcessing = []string{}
		daemon.BatchCount = 0
		sqlite.TrimHeight(starting_height)
		if daemon.Status.ErrorCount != int64(0) {
			show.NewMessage(show.Message{
				Vars: []any{
					strconv.Itoa(int(daemon.Status.ErrorCount)) + " Error(s) detected! Type:",
					daemon.Status.ErrorType + " Name:" + daemon.Status.ErrorName + " Details:" + daemon.Status.ErrorDetail,
				},
			})
		}
	}
	daemon.Blocks = []daemon.Block{} //clear here
	daemon.Batches = []daemon.Batch{}
	//daemon.Cancels = map[int]context.CancelFunc{}
	daemon.AssignConnections(daemon.Status.ErrorCount != int64(0)) //might as well check/retry new connections here
	daemon.Status.ErrorCount = 0
	daemon.StartingFrom = int(starting_height)
	/*
		if starting_height >= 150000 && starting_height < 170000 {
			memBatchSize = int16(500)
		} else {
			memBatchSize = int16(100)
		}
	*/
	sqlindexer = NewSQLIndexer(sqlite, starting_height, CustomActions)
	show.NewMessage(show.Message{Text: "Topo Height ", Vars: []any{HighestKnownHeight}})
	show.NewMessage(show.Message{Text: "Last Height ", Vars: []any{fmt.Sprint(starting_height)}})

	if TargetHeight < HighestKnownHeight-blockBatchSize && starting_height+blockBatchSize < HighestKnownHeight {
		TargetHeight = starting_height + blockBatchSize
	} else {
		TargetHeight = HighestKnownHeight
	}

	var wg sync.WaitGroup
	for bheight := starting_height; bheight < TargetHeight; bheight++ {
		if !daemon.OK() {
			break
		}
		//---- MAIN PRINTOUT
		showBlockStatus(bheight)
		daemon.Ask("height")
		wg.Add(1)
		daemon.Mutex.Lock()
		daemon.Blocks = append(daemon.Blocks, daemon.Block{Height: bheight})
		daemon.Mutex.Unlock()
		go ProcessBlock(&wg, bheight)
		checkGo()
	}

	wg.Wait()

	//check if there was a missing request or a db error
	if !daemon.OK() { //Start over from last saved.
		start_gnomon_indexer() //without saving index height
		return
	}
	// Wait for all requests to finish
	show.NewMessage(show.Message{Text: "Batch completing, count:", Vars: []any{blockBatchSize}})
	place := 0
	count := 0
	for {
		loading := []string{" .. .", ". .. ", ".. .."}
		fmt.Print("\n", loading[place], "\r")

		place++
		if place == 3 {
			place = 0
		}
		count++
		//Mutex.Lock()
		if (daemon.BatchCount == 0 && len(daemon.TXIDSProcessing) == 0) || count > 120 || !daemon.OK() { // wait for 2 mins(longer than timeout etc...)
			break
		}
		if len(daemon.TXIDSProcessing) != 0 {
			fmt.Println(daemon.TXIDSProcessing)
		}
		w, _ := time.ParseDuration("1s")
		time.Sleep(w)
	}

	if count <= 120 && daemon.OK() {
		sqlite.StoreLastIndexHeight(TargetHeight)
	}

	last := HighestKnownHeight
	HighestKnownHeight = daemon.GetTopoHeight()
	if HighestKnownHeight < 1 {
		daemon.AssignConnections(true)

		show.NewMessage(show.Message{Text: "Error getting height ....", Vars: []any{HighestKnownHeight}})
		HighestKnownHeight = daemon.GetTopoHeight()
		if HighestKnownHeight < 1 {
			panic("Too many failed connections")
		}
	}
	show.NewMessage(show.Message{Text: "Last:", Vars: []any{last}})
	show.NewMessage(show.Message{Text: "TargetHeight:", Vars: []any{TargetHeight}})

	//maybe skip when caught up
	show.NewMessage(show.Message{Text: "Purging spam:", Vars: []any{sql.Spammers}})

	sqlite.RidSpam()

	var switching = false
	if UseMem {
		show.NewMessage(show.Message{Text: "Saving Batch...... ", Vars: []any{fileSizeMB(sqlite.Db_path), "MB"}})
		sqlite.WriteToDisk()
		//Check size
		if int64(RamSizeMB) <= fileSizeMB(sqlite.Db_path) {
			switching = true
			sqlite.DB.Close()
			show.NewMessage(show.Message{Text: "Switching to disk mode...... ", Vars: []any{TargetHeight}})
		}
	}

	if TargetHeight == last || switching {
		if !switching {
			show.NewMessage(show.Message{Text: "All caught up...... ", Vars: []any{TargetHeight}})
			t, _ := time.ParseDuration("5s")
			time.Sleep(t)
		}
		//Don't use mem when caught up or over limit
		UseMem = false
		blockBatchSize = blockBatchSizeDisk
		filename := filepath.Base(sqlite.Db_path)
		dir := filepath.Dir(sqlite.Db_path)
		sqlite, err = sql.NewDiskDB(dir, filename)
	}
	show.NewMessage(show.Message{Text: "Saving phase over......"})
	sqlite.ViewTables()

	start_gnomon_indexer()

}

var counter = 0

func ProcessBlock(wg *sync.WaitGroup, bheight int64) {
	defer wg.Done()
	if !daemon.OK() {
		return
	}
	discarding := false

	result := daemon.GetBlockInfo(rpc.GetBlock_Params{
		Height: uint64(bheight),
	})
	if !daemon.OK() {
		return
	}
	bl := daemon.GetBlockDeserialized(result.Blob)

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

	daemon.Mutex.Lock()
	if !discarding {
		daemon.BlockByHeight(bheight).TxIds = append(daemon.BlockByHeight(bheight).TxIds, tx_str_list...)
		daemon.TXIDSProcessing = append(daemon.TXIDSProcessing, tx_str_list...)
	} else {
		daemon.RemoveBlocks(int(bheight))
	}
	txidlen := len(daemon.TXIDSProcessing)
	if int16(txidlen) >= batchSize || (len(daemon.Batches) == 0 && txidlen != 0) {
		var batches = []daemon.Batch{}
		var wga sync.WaitGroup
		//Find total number of batches
		batch_count := int(math.Ceil(float64(txidlen) / float64(batchSize)))
		for i := range batch_count {
			i++ //lmao
			end := batchSize
			if len(daemon.Batches) == 0 && len(daemon.TXIDSProcessing) != 0 {
				if int16(len(daemon.TXIDSProcessing)) < batchSize {
					end = int16(len(daemon.TXIDSProcessing))
				}
			}
			txs := daemon.TXIDSProcessing[:end]
			daemon.TXIDSProcessing = daemon.TXIDSProcessing[end:]
			batches = append(batches, daemon.Batch{TxIds: txs})
		}
		daemon.Mutex.Unlock()
		for i := range batches {
			atomic.AddInt32(&rcount, 1)
			checkGo()
			wga.Add(1)
			go DoBatch(&wga, batches[i])
		}
		wga.Wait()
	} else {
		daemon.Mutex.Unlock()
	}
}

var laststored = int64(0)

func DoBatch(wga *sync.WaitGroup, batch daemon.Batch) {
	defer wga.Done()
	defer atomic.AddInt32(&rcount, -1)
	if !daemon.OK() {
		return
	}
	daemon.Mutex.Lock()
	daemon.BatchCount++
	daemon.Mutex.Unlock()
	var wg2 sync.WaitGroup
	var r rpc.GetTransaction_Result
	daemon.Ask("tx")
	r = daemon.GetTransaction(rpc.GetTransaction_Params{
		Tx_Hashes: batch.TxIds, //[int(batchSize)*i : end]
	})

	showBlockStatus(-1)
	if !daemon.OK() {
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
			daemon.RemoveTXs(remove)
			updateBlocks(daemon.Batch{
				TxIds: remove,
			})
		}
	}
	wg2.Wait()

	if daemon.OK() {
		daemon.Mutex.Lock()
		daemon.BatchCount--
		daemon.RemoveTXs(batch.TxIds)
		daemon.Mutex.Unlock()
		updateBlocks(batch)
	}
}
func saveDetails(wg2 *sync.WaitGroup, tx transaction.Transaction, bheight int64, signer string, batch daemon.Batch) { //, large bool
	defer wg2.Done()
	if !daemon.OK() {
		return
	}
	var wg3 sync.WaitGroup
	ok := true
	txhash := tx.GetHash().String()
	daemon.RemoveTXs([]string{txhash})

	if tx.TransactionType != transaction.SC_TX { //|| (len(tx.Payloads) > 10 && tx.Payloads[0].RPCType == byte(transaction.REGISTRATION))
		ok = false
	}
	//fmt.Print("scid found at height:", fmt.Sprint(bheight)+"\n")

	tx_type := ""
	params := rpc.GetSC_Params{}
	if tx.SCDATA.HasValue(rpc.SCCODE, rpc.DataString) {
		tx_type = "install"
		show.NewMessage(show.Message{Text: "SC Code:", Vars: []any{tx.SCDATA.Value(rpc.SCCODE, rpc.DataString)}})
		params.SCID = txhash
	} else if tx.SCDATA.HasValue(rpc.SCID, rpc.DataHash) {
		tx_type = "invoke"
		//	fmt.Println("invoke:", tx)
		scid, ok := tx.SCDATA.Value(rpc.SCID, rpc.DataHash).(crypto.Hash)
		params.SCID = scid.String()
		if !ok || params.SCID == "" {
			ok = false
		}
	}

	// Discard the discardable
	if CustomActions[params.SCID].Act == "discard" ||
		(CustomActions[params.SCID].Act == "discard-before" && CustomActions[params.SCID].Block >= bheight) {
		ok = false
	} else if (slices.Contains(sql.Spammers, signer)) && params.SCID == Hardcoded_SCIDS[0] { //Not great
		ok = false
	}

	if ok {
		// Finish filling the required values
		if tx_type == "install" {
			params.Code = true
			params.Variables = true
			params.TopoHeight = bheight
		} else if tx_type == "invoke" {
			params.Code = false
			params.Variables = CustomActions[txhash].Act != "saveasinteraction"
			params.TopoHeight = bheight
		}

		wg3.Add(1)
		go processSCs(&wg3, tx, tx_type, params, bheight, signer)
		wg3.Wait()
	}

	updateBlocks(daemon.Batch{
		TxIds: []string{txhash},
	})

}

func processSCs(wg3 *sync.WaitGroup, tx transaction.Transaction, tx_type string, params rpc.GetSC_Params, bheight int64, signer string) {
	defer wg3.Done()
	if !daemon.OK() {
		return
	}
	daemon.Ask("sc")
	sc := daemon.GetSC(params) //Variables: true,
	if !daemon.OK() {          //|| sc.Status != "OK"
		return
	}
	vars, err := GetSCVariables(sc.VariableStringKeys, sc.VariableUint64Keys)
	if err != nil { //might be worth investigating what errors could occur
		return
	}

	kv := sc.VariableStringKeys

	//fmt.Println("key", kv)
	scname := daemon.GetSCNameFromVars(kv)
	scdesc := ""
	scimgurl := ""
	tags := ""
	class := ""
	if params.SCID != Hardcoded_SCIDS[0] { //only need the name for these
		scdesc = daemon.GetSCDescriptionFromVars(kv)
		scimgurl = daemon.GetSCIDImageURLFromVars(kv)
		for key, name := range Filters {
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

	staged := structs.SCIDToIndexStage{
		Type:       tx_type,
		TXHash:     tx.GetHash().String(),
		Fsi:        &structs.FastSyncImport{Height: uint64(bheight), Signer: signer, SCName: scname, SCDesc: scdesc, SCImgURL: scimgurl}, //
		ScVars:     vars,
		ScCode:     sc.Code,
		Params:     params,
		Entrypoint: entrypoint,
		Class:      class, //Class and tags are not in original gnomon
		Tags:       tags,
	}

	//fmt.Println("staged scid:", staged.TXHash, ":", fmt.Sprint(staged.Fsi.Height))
	//fmt.Println("staged params.scid:", params.SCID, ":", fmt.Sprint(staged.Fsi.Height))

	// now add the scid to the index
	sql.Ask()
	// if the contract already exists, record the interaction

	if err := sqlindexer.AddSCIDToIndex(staged); err != nil {
		show.NewMessage(show.Message{Vars: []any{err, " ", staged.TXHash, " ", staged.Fsi.Height}})
		if strings.Contains(err.Error(), "database is locked") {
			daemon.NewError("database", "db lock", "Adding index")
		}
	}

}

func storeHeight(bheight int64) {
	if !daemon.OK() {
		return
	}
	sql.Ask()
	//fmt.Println("Saving LastIndexHeight: ", bheight)
	if ok, err := sqlindexer.SSSBackend.StoreLastIndexHeight(int64(bheight)); !ok && err != nil {
		show.NewMessage(show.Message{Text: "Error Saving LastIndexHeight: ", Vars: []any{err}})
		if strings.Contains(err.Error(), "database is locked") {
			daemon.NewError("database", "db lock", "Storing last index")
		}
	}
}

func decodeTx(tx_hex string) (transaction.Transaction, error) {
	b, err := hex.DecodeString(tx_hex)
	if err != nil {
		panic(err)
	}
	var tx transaction.Transaction
	if err := tx.Deserialize(b); err != nil {
		show.NewMessage(show.Message{Text: "TX Height:", Vars: []any{tx.Height}})
		if strings.Contains(err.Error(), "Invalid Version in Transaction") ||
			strings.Contains(err.Error(), "Transaction version unknown") {
			return tx, err
		}
		panic(err)
	}
	return tx, err
}

// Save at a contiguous point
func updateBlocks(batch daemon.Batch) {
	daemon.Mutex.Lock()
	var remove = []int64{}
	for _, block := range daemon.Blocks {
		if block.Processed || block.Height == 0 {
			remove = append(remove, block.Height)
		}
	}
	for height := range remove {
		daemon.RemoveBlocks(int(height))
	}
	daemon.Mutex.Unlock()
	for _, block := range daemon.Blocks {
		if block.Height > laststored {
			storeHeight(block.Height)
			laststored = block.Height
			break
		}
	}
}

/********************************/
/*********** Helpers ************/
/********************************/
// Can be used to limit the amount of some of the waitgroups
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

// Check indexable height at daemon
func findStart(start int64, top int64) (block int64) {
	difference := top - start
	offset := difference / 2
	if top-start == 1 {
		return top - 1
	}
	if daemon.GetBlockInfo(rpc.GetBlock_Params{Height: uint64(block)}).Status == "OK" {
		return findStart(start, offset+start)
	} else {
		return findStart(offset+start, top)
	}
}
func fileSizeMB(filePath string) int64 {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return 0
	}
	sizeBytes := fileInfo.Size()
	return int64(float64(sizeBytes) / (1024 * 1024))
}

func showBlockStatus(height int64) {
	_, t := getOutCounts()
	show.ShowBlockStatus(height, getSpeed(), t)
}

// vars for speed calulations
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
func getOutCounts() (int, string) {
	text := ""
	spacer := ""
	tot := 0
	if daemon.PreferredRequests >= 10 {
		spacer = " "
	}
	for i, out := range daemon.HeightOuts {
		insert := ""

		total := int(daemon.HeightOuts[i]) + int(daemon.TxOuts[i]) + int(daemon.SCOuts[i])
		if total < 10 {
			insert = spacer
		}
		text += ":" + insert + strconv.Itoa(int(total))
		tot += int(out)
	}
	if len(text) > 1 {
		text = text[1:]
	}
	return tot, text
}

/********************************/
// ...
/********************************/
/*
fmt.Println("batch.TxIds:", len(batch.TxIds))

	fmt.Println("daemon.BlockByHeight(bheight).TxIds:", len(daemon.BlockByHeight(bheight).TxIds))
	for _, t := range batch.TxIds {

		if slices.Contains(daemon.BlockByHeight(bheight).TxIds, t) {
			fmt.Println(bheight, " (bheight).TxIds Contains:", t)
		}
		if !slices.Contains(daemon.BlockByHeight(bheight).TxIds, t) {
			fmt.Println(bheight, " (bheight).TxIds NOT Contains:", t)
		}

		daemon.ProcessBlocks(t) //daemon.RemoveTXs(batch.TxIds)
}
	if len(tx.Txs_as_hex) != len(batch.TxIds) {
		fmt.Println(len(r.Txs_as_hex), " fffffff ", len(batch.TxIds))
		for i, _ := range r.Txs_as_hex {
			fmt.Println(int64(r.Txs[i].Block_Height), " - ", r.Txs[i])
		}
		panic(r)
	}

*/
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
