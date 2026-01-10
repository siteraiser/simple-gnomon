package api

import (
	"encoding/json"
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

func Start(port string) {

	go func() {
		time.Sleep(200 * time.Millisecond)
		log.Println("Server listening on port " + port)
	}()
	db_name := fmt.Sprintf("sql%s.db", "GNOMON")
	wd := globals.GetDataDirectory()
	db_path := filepath.Join(wd, "gnomondb")
	sqlite, _ = sql.NewDiskDB(db_path, db_name)

	http.HandleFunc("/GetLastIndexHeight", GetLastIndexHeight)
	http.HandleFunc("/GetAllOwnersAndSCIDs", GetAllOwnersAndSCIDs)
	http.HandleFunc("/GetAllSCIDVariableDetails", GetAllSCIDVariableDetails)
	http.HandleFunc("/GetSCIDVariableDetailsAtTopoheight", GetSCIDVariableDetailsAtTopoheight)
	http.HandleFunc("/GetSCIDInteractionHeight", GetSCIDInteractionHeight)
	http.HandleFunc("/GetSCIDsByClass", GetSCIDsByClass)
	http.HandleFunc("/GetSCIDsByTags", GetSCIDsByTags)

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

// Check Gnomon indexed height
// http://localhost:8080/GetLastIndexHeight
func GetLastIndexHeight(w http.ResponseWriter, r *http.Request) {
	head(w)
	index, _ := sqlite.GetLastIndexHeight()
	jsonData, _ := json.Marshal(index)
	fmt.Fprint(w, string(jsonData))
}

// Large request
// http://localhost:8080/GetAllOwnersAndSCIDs
func GetAllOwnersAndSCIDs(w http.ResponseWriter, r *http.Request) {
	head(w)
	jsonData, _ := json.Marshal(sqlite.GetAllOwnersAndSCIDs())
	fmt.Fprint(w, string(jsonData))
}

// http://localhost:8080/GetAllSCIDVariableDetails?scid=b77b1f5eeff6ed39c8b979c2aeb1c800081fc2ae8f570ad254bedf47bfa977f0
func GetAllSCIDVariableDetails(w http.ResponseWriter, r *http.Request) {
	head(w)
	jsonData, _ := json.Marshal(sqlite.GetAllSCIDVariableDetails(QueryParam("scid", r.URL.RawQuery)))
	fmt.Fprint(w, string(jsonData))
}

// http://localhost:8080/GetSCIDVariableDetailsAtTopoheight?scid=805ade9294d01a8c9892c73dc7ddba012eaa0d917348f9b317b706131c82a2d5&height=50000
func GetSCIDVariableDetailsAtTopoheight(w http.ResponseWriter, r *http.Request) {
	head(w)
	h, _ := strconv.Atoi(QueryParam("height", r.URL.RawQuery))
	jsonData, _ := json.Marshal(sqlite.GetSCIDVariableDetailsAtTopoheight(QueryParam("scid", r.URL.RawQuery), int64(h)))
	fmt.Fprint(w, string(jsonData))
}

// http://localhost:8080/GetSCIDInteractionHeight?scid=b77b1f5eeff6ed39c8b979c2aeb1c800081fc2ae8f570ad254bedf47bfa977f0
func GetSCIDInteractionHeight(w http.ResponseWriter, r *http.Request) {
	head(w)
	jsonData, _ := json.Marshal(sqlite.GetSCIDInteractionHeight(QueryParam("scid", r.URL.RawQuery)))
	fmt.Fprint(w, string(jsonData))
}

// http://localhost:8080/GetSCIDsByClass?class=tela
func GetSCIDsByClass(w http.ResponseWriter, r *http.Request) {
	head(w)
	jsonData, _ := json.Marshal(sqlite.GetSCsByClass(QueryParam("class", r.URL.RawQuery)))
	fmt.Fprint(w, string(jsonData))
}

// Returns a map of scids attached
// http://localhost:8080/GetSCIDsByTags?tags=G45-AT&tags=G45-C
func GetSCIDsByTags(w http.ResponseWriter, r *http.Request) {
	head(w)
	query := r.URL.Query()
	res := sqlite.GetSCsByTags(query["tags"])
	jsonData, _ := json.Marshal(res)
	fmt.Fprint(w, string(jsonData))
}
