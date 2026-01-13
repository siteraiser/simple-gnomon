package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/deroproject/derohe/globals"
	"github.com/secretnamebasis/simple-gnomon/api"
	"github.com/secretnamebasis/simple-gnomon/daemon"
	sql "github.com/secretnamebasis/simple-gnomon/db"
	"github.com/secretnamebasis/simple-gnomon/show"
)

type Configuration struct {
	RamSizeMB   int
	SpamLevel   string
	Smoothing   int
	DisplayMode int
	Filters     map[string]map[string][]string
	Endpoints   []daemon.Connection
	Port        string
}

var Filters map[string]map[string][]string
var defaultFilters = map[string]map[string][]string{
	"g45": {
		"tags":    {"G45-AT", "G45-C", "G45-FAT", "G45-NAME", "T345"},
		"options": {"b", "i"}, //regex filters for word boundry and c.i. matching
	},
	"nfa":   {"tags": {"ART-NFA-MS1"}},
	"swaps": {"tags": {"StartSwap"}},
	"tela":  {"tags": {"docVersion", "telaVersion"}},
}

func dbName() (db_path string, db_name string) {
	db_name = fmt.Sprintf("sql%s.db", "GNOMON")
	wd := globals.GetDataDirectory()
	db_path = filepath.Join(wd, "gnomondb")
	return
}
func initDB() {
	//Create the tables now...
	sqlite, _ := sql.NewDiskDB(dbName())
	sql.CreateTables(sqlite.DB)
	sqlite.DB.Close()
}
func getConfig(update bool) Configuration {

	config := Configuration{}
	var text string
	sqlite, _ := sql.NewDiskDB(dbName())
	defer sqlite.DB.Close()
	// Ram settings
	val, err := sql.LoadSetting(sqlite.DB, "RamSizeMB")
	if val == "" || update {
		print("Enter system memory to use in GB(0,2,8,...):")
		_, err = fmt.Scanln(&text)
		config.RamSizeMB, _ = strconv.Atoi(text)
		config.RamSizeMB *= int(1024)
		if err != nil {
			println("Error:", err)
			panic("Use integer for value")
		}
		sql.SaveSetting(sqlite.DB, "RamSizeMB", strconv.Itoa(config.RamSizeMB))
	} else {
		print(val, "MB of ram being used")
		config.RamSizeMB, _ = strconv.Atoi(val)
	}

	// Smoothing settings
	val, err = sql.LoadSetting(sqlite.DB, "Smoothing")
	if val == "" || update {
		print("Use smoothing? 0-1000:")
		_, err = fmt.Scanln(&text)
		if err != nil {
			println("Error:", err)
			panic("Use integer for value")
		}
		sql.SaveSetting(sqlite.DB, "Smoothing", text)
	} else {
		println("Using Saved Smoothing Period:", val)
		config.Smoothing, _ = strconv.Atoi(val)
	}

	// Spam settings
	val, err = sql.LoadSetting(sqlite.DB, "SpamLevel")
	if val == "" || update {
		println("SC spam threshold 0-50 recommended")
		print("Enter number of name registrations allowed per wallet:")
		_, err = fmt.Scanln(&text)
		if err != nil {
			println("Error:", err)
			panic("Use integer for value")
		}
		config.SpamLevel = text
		sql.SaveSetting(sqlite.DB, "SpamLevel", text)
	} else {
		println("Using Smoothing Level: ", val)
		config.SpamLevel = val
	}

	// Endpoint/daemon connections
	val, err = sql.LoadSetting(sqlite.DB, "Endpoints")
	if val == "" || update {
		fmt.Println("Enter custom connection or enter n to use the default remote connections eg. node.derofoundation.org:10102 ")
		_, err = fmt.Scanln(&text)
		if text != "n" {
			daemon.Endpoints = []daemon.Connection{
				{Address: text},
			}
			sql.SaveSetting(sqlite.DB, "Endpoints", text)
		} else {
			sql.SaveSetting(sqlite.DB, "Endpoints", "")
		}
	} else {
		println("Using Connections: ", val)
		daemon.Endpoints = []daemon.Connection{
			{Address: val},
		}
		config.Endpoints = daemon.Endpoints
	}

	// Filters

	val, err = sql.LoadSetting(sqlite.DB, "Filters")
	if val == "" || update {
		fmt.Println("Edit filters? y or n")
		_, err = fmt.Scanln(&text)
		if text == "n" {
			if val == "" {
				Filters = defaultFilters
			} else {
				var temp map[string]map[string][]string
				json.Unmarshal([]byte(val), &temp)
				Filters = temp
			}
		} else if text == "y" {
			var temp map[string]map[string][]string
			if val == "" {
				Filters = editFilters(defaultFilters)
				bytes, _ := json.Marshal(Filters)
				val = string(bytes)
				sql.SaveSetting(sqlite.DB, "Filters", val)
			} else {
				json.Unmarshal([]byte(val), &temp)
				Filters = editFilters(temp)
				bytes, _ := json.Marshal(Filters)
				val = string(bytes)
				sql.SaveSetting(sqlite.DB, "Filters", val)
			}

		}
	} else {
		if val != "" {
			var temp map[string]map[string][]string
			json.Unmarshal([]byte(val), &temp)
			Filters = temp
		} else {
			Filters = defaultFilters
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
		fmt.Println("Enter a port number for the api or n to skip:")
		_, err = fmt.Scanln(&text)
		if _, err := strconv.Atoi(text); err == nil && text != "n" {
			go api.Start(text)
			time.Sleep(200 * time.Millisecond)
		}
	}

	fmt.Println("Choose display mode, 0, 1 or 2:")
	_, err = fmt.Scanln(&text)
	show.DisplayMode, _ = strconv.Atoi(text)

	return config
}

func editFilters(filters map[string]map[string][]string) map[string]map[string][]string {
	fmt.Println("-- Filters: ")
	for class, filter := range filters {
		fmt.Println("Class:", class, "----------------------------------------------------")
		fmt.Println("Filter:", filter)
	}
	fmt.Println("--------------------------------------------")
	reader := bufio.NewReader(os.Stdin)
	fmt.Print(`Type the name of the class of filter to edit or "add" or "delete classname" or "done" to return:`)
	text, err := reader.ReadString('\n')
	if err != nil {
		println("Error reading input:", err)
	}
	text = strings.TrimSpace(text)
	reader.Reset(os.Stdin)

	if text == "done" {
		return filters
	} else if len(text) > 3 && strings.Contains(text[:3], "add") {
		f := map[string][]string{}
		filters[text[4:]] = f
		return editFilters(filters)
	} else if len(text) > 6 && strings.Contains(text[:6], "delete") {
		if _, exists := filters[text[7:]]; exists {
			delete(filters, text[7:])
			fmt.Printf("'%s' deleted.\n", text[7:])
		}
		return editFilters(filters)
	}
	filters[text] = editFilter(filters[text])
	return editFilters(filters)
}
func editFilter(filter map[string][]string) map[string][]string {
	var text string
	println("Enter 1 to edit filter or 2 for options:")
	_, _ = fmt.Scanln(&text)
	if text == "1" {
		filter = changeTags(filter)
	} else {
		filter = changeOption(filter)
	}
	return filter
}
func changeTags(filter map[string][]string) map[string][]string {
	fmt.Println("Current tags:", strings.Join(filter["tags"], ","))
	var text string
	println("Enter new csv list of tags or type done to return:")
	_, _ = fmt.Scanln(&text)
	if text != "done" {
		filter["tags"] = strings.Split(text, ",")
	}
	return filter
}
func changeOption(option map[string][]string) map[string][]string {
	fmt.Println("Current options:", option["options"])
	var text string
	println(`"i" is case insensitve match and "b" is a word boudry match.`)
	println(`Enter new csv list of options eg, "i,b", or "i" or type done to return:`)

	_, _ = fmt.Scanln(&text)
	if text != "done" {
		option["options"] = strings.Split(text, ",")
		if len(option["options"]) == 0 {
			delete(option, "options")
		}
	}
	return option
}
