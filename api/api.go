package api

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/deroproject/derohe/globals"
	sql "github.com/secretnamebasis/simple-gnomon/db"
	sqldb "github.com/secretnamebasis/simple-gnomon/db"
)

var sqlite = &sqldb.SqlStore{}

func Start() {
	portFlag := flag.Int("port", 8080, "string")
	flag.Parse()
	port := strconv.Itoa(*portFlag)
	go func() {
		time.Sleep(200 * time.Millisecond)
		log.Println("Server listening on port " + port)
	}()
	db_name := fmt.Sprintf("sql%s.db", "GNOMON")
	wd := globals.GetDataDirectory()
	db_path := filepath.Join(wd, "gnomondb")
	sqlite, _ = sql.NewDiskDB(db_path, db_name)
	http.HandleFunc("/GetAllOwnersAndSCIDs", GetAllOwnersAndSCIDs)
	http.ListenAndServe("localhost:"+port, nil)
}

// http://localhost:8080/GetAllOwnersAndSCIDs
func GetAllOwnersAndSCIDs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Accept", "application/json; charset=utf-8")
	jsonData, err := json.Marshal(sqlite.GetAllOwnersAndSCIDs())
	if err != nil {
		fmt.Println("Error encoding JSON:", err)
		return
	}
	fmt.Fprint(w, string(jsonData))
}
