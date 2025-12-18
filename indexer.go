package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
)

type SCTXParse struct {
	Txid       string
	Scid       string
	Scid_hex   []byte
	Entrypoint string
	Method     string
	Sc_args    rpc.Arguments
	Sender     string
	Payloads   []transaction.AssetPayload
	Fees       uint64
	Height     int64
}

type SCIDVariable struct {
	Key   any
	Value any
}

type FastSyncImport struct {
	Owner    string
	Height   uint64
	SCName   string
	SCDesc   string
	SCImgURL string
}

type SCIDToIndexStage struct {
	Scid   string
	Fsi    *FastSyncImport
	ScVars []*SCIDVariable
	ScCode string
	ScSCID string
	Class  string
	Tags   string
}

type Indexer struct {
	LastIndexedHeight int64
	ChainHeight       int64
	SearchFilter      []string
	CustomActions     map[string]action
	SSSBackend        *SqlStore
	ValidatedSCs      []string
	Status            string
}

func NewSQLIndexer(Sqls_backend *SqlStore, last_indexedheight int64, CustomActions map[string]action) *Indexer {
	return &Indexer{
		LastIndexedHeight: last_indexedheight,
		CustomActions:     CustomActions,
		SSSBackend:        Sqls_backend,
	}
}

// Manually add/inject a SCID to be indexed. Checks validity and then stores within owner tree (no signer addr) and stores a set of current variables.
func (indexer *Indexer) AddSCIDToIndex(scidstoadd SCIDToIndexStage) (err error) {
	//	fmt.Println("Adding to Index: ", scidstoadd)
	if scidstoadd.Scid == "" {
		return errors.New("no scid")
	}

	if scidstoadd.Fsi == nil {
		return errors.New("nothing to import")
	}

	changed := false
	ownerstored := false
	//	fmt.Printf("SCIDS TO ADD: %v...", scidstoadd.ScVars)
	// By returning valid variables of a given Scid (GetSC --> parse vars), we can conclude it is a valid SCID. Otherwise, skip adding to validated scids
	if len(scidstoadd.ScVars) != 0 {

		changed, err = indexer.SSSBackend.StoreSCIDVariableDetails(
			scidstoadd.Scid,
			scidstoadd.ScVars,
			int64(scidstoadd.Fsi.Height),
		)
		if err != nil {
			fmt.Println("err StoreSCIDVariableDetails: ", err)
			return err
		}
		if !changed {
			return errors.New("did not store scid/vars")
		}
		if scidstoadd.ScCode != "" {
			ownerstored, err = indexer.SSSBackend.StoreOwner(
				scidstoadd.Scid,
				scidstoadd.Fsi.Owner,
				scidstoadd.Fsi.SCName,
				scidstoadd.Fsi.SCDesc,
				scidstoadd.Fsi.SCImgURL,
				scidstoadd.Class,
				scidstoadd.Tags,
			)

			if err != nil {
				fmt.Println("err StoreOwner: ", err)
				return err
			}
		}

	}

	if !ownerstored || len(scidstoadd.ScVars) == 0 {
		txid := scidstoadd.Scid
		scid := scidstoadd.ScSCID
		//no name spams
		if indexer.CustomActions[scid].Act == "saveasinteraction" {
			//not saving the vars for the name contract
			scid = scidstoadd.Scid
			txid = scidstoadd.ScSCID
		}

		if scid == Hardcoded_SCIDS[0] {
			//not saving the vars for the name contract
			scid = scidstoadd.Scid
			txid = scidstoadd.ScSCID
		}
		//was not an install or a failed install
		changed, err = indexer.SSSBackend.StoreSCIDInteractionHeight(
			txid, //really the txid in this instance
			scid,
			//	scidstoadd.ScCode,
			int64(scidstoadd.Fsi.Height),
		)
		if err != nil {
			return err
		}

		if !changed {
			return errors.New("did not store scid/interaction")
		}
		if UseMem {
			fmt.Print("sql [AddSCIDToIndex] New updated disk: ", fmt.Sprint(len(indexer.SSSBackend.GetSCIDInteractionHeight(scidstoadd.Scid))))
		}

		return
	}

	return nil
}

// Gets SC variable details
func GetSCVariables(keysstring map[string]any, keysuint64 map[uint64]any) (variables []*SCIDVariable, err error) {
	//balances = make(map[string]uint64)
	fmt.Println(keysuint64)

	isAlpha := regexp.MustCompile(`^[A-Za-z]+$`).MatchString

	for k, v := range keysstring {
		currVar := &SCIDVariable{}
		currVar.Key = k
		switch cval := v.(type) {
		case float64:
			currVar.Value = uint64(cval)
		case uint64:
			currVar.Value = cval
		case string:
			// hex decode since all strings are hex encoded
			dstr, _ := hex.DecodeString(cval)
			// Check if dstr is an address raw
			p := new(crypto.Point)
			if err := p.DecodeCompressed(dstr); err == nil {

				addr := rpc.NewAddressFromKeys(p)
				currVar.Value = addr.String()
			} else {
				// Check specific patterns which reflect STORE() operations of TXID(), SCID(), etc.
				str := string(dstr)
				if len(str) == crypto.HashLength {
					var h crypto.Hash
					copy(h[:crypto.HashLength], []byte(str)[:])

					if len(h.String()) == 64 && !isAlpha(str) {
						if !crypto.HashHexToHash(str).IsZero() {
							currVar.Value = str
						} else {
							currVar.Value = h.String()
						}
					} else {
						currVar.Value = str
					}
				} else {
					currVar.Value = str
				}
			}
		default:
			// non-string/uint64 (shouldn't be here actually since it's either uint64 or string conversion)
			str := fmt.Sprintf("%v", cval)
			// Check specific patterns which reflect STORE() operations of TXID(), SCID(), etc.
			if len(str) == crypto.HashLength {
				var h crypto.Hash
				copy(h[:crypto.HashLength], []byte(str)[:])

				if len(h.String()) == 64 && !isAlpha(str) {
					if !crypto.HashHexToHash(str).IsZero() {
						currVar.Value = str
					} else {
						currVar.Value = h.String()
					}
				} else {
					currVar.Value = str
				}
			} else {
				currVar.Value = str
			}
		}
		variables = append(variables, currVar)
	}

	for k, v := range keysuint64 {
		currVar := &SCIDVariable{}
		currVar.Key = k
		switch cval := v.(type) {
		case string:
			// hex decode since all strings are hex encoded
			decd, _ := hex.DecodeString(cval)
			p := new(crypto.Point)
			if err := p.DecodeCompressed(decd); err == nil {

				addr := rpc.NewAddressFromKeys(p)
				currVar.Value = addr.String()
			} else {
				// Check specific patterns which reflect STORE() operations of TXID(), SCID(), etc.
				str := string(decd)
				if len(str) == crypto.HashLength {
					var h crypto.Hash
					copy(h[:crypto.HashLength], []byte(str)[:])

					if len(h.String()) == 64 && !isAlpha(str) {
						if !crypto.HashHexToHash(str).IsZero() {
							currVar.Value = str
						} else {
							currVar.Value = h.String()
						}
					} else {
						currVar.Value = str
					}
				} else {
					currVar.Value = str
				}
			}
		case uint64:
			currVar.Value = cval
		case float64:
			currVar.Value = uint64(cval)
		default:
			// non-string/uint64 (shouldn't be here actually since it's either uint64 or string conversion)
			str := fmt.Sprintf("%v", cval)
			// Check specific patterns which reflect STORE() operations of TXID(), SCID(), etc.
			if len(str) == crypto.HashLength {
				var h crypto.Hash
				copy(h[:crypto.HashLength], []byte(str)[:])

				if len(h.String()) == 64 && !isAlpha(str) {
					if !crypto.HashHexToHash(str).IsZero() {
						currVar.Value = str
					} else {
						currVar.Value = h.String()
					}
				} else {
					currVar.Value = str
				}
			} else {
				currVar.Value = str
			}
		}
		variables = append(variables, currVar)
	}

	return variables, nil
}
