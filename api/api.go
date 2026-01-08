package api

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"time"

	"github.com/deroproject/derohe/globals"
	sql "github.com/secretnamebasis/simple-gnomon/db"
)

var sqlite = &sql.SqlStore{}

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
	http.HandleFunc("/GetSCIDVariableDetailsAtTopoheight", GetSCIDVariableDetailsAtTopoheight)

	http.ListenAndServe("localhost:"+port, nil)
}
func head(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Accept", "application/x-www-form-urlencoded; charset=utf-8")
}

// Returns a query parameter value by key
func QueryParam(i string, query string) string {
	parsedURL, err := url.Parse("?" + query)
	if err != nil {
		fmt.Println("Error:", err)
		return ""
	}
	queryParams := parsedURL.Query()
	return queryParams.Get(i)
}

// http://localhost:8080/GetAllOwnersAndSCIDs
func GetAllOwnersAndSCIDs(w http.ResponseWriter, r *http.Request) {
	head(w)
	jsonData, err := json.Marshal(sqlite.GetAllOwnersAndSCIDs())
	if err != nil {
		fmt.Println("Error encoding JSON:", err)
		return
	}
	fmt.Fprint(w, string(jsonData))
}

// http://localhost:8080/GetSCIDVariableDetailsAtTopoheight
// http://localhost:8080/GetSCIDVariableDetailsAtTopoheight?scid=fa71110e1b25a4dd0f3fddd2ed9ed5d35a5a332ddf34e93736feef65a428f3ad&height=4000
// POST not yet implemented
// Or use POST vars "scid" and "height"
func GetSCIDVariableDetailsAtTopoheight(w http.ResponseWriter, r *http.Request) {
	head(w)
	h, _ := strconv.Atoi(QueryParam("height", r.URL.RawQuery))
	jsonData, err := json.Marshal(sqlite.GetSCIDVariableDetailsAtTopoheight(QueryParam("scid", r.URL.RawQuery), int64(h)))
	if err != nil {
		fmt.Println("Error encoding JSON:", err)
		return
	}
	fmt.Fprint(w, string(jsonData))
}
