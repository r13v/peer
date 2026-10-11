package board

import (
	"embed"
	"encoding/json"
)

//go:embed schema/*.json
var schemaFS embed.FS

// Schemas are the JSON Schemas (draft 2020-12) of board.json and of CLI
// and browser packs, as peer board schema prints them.
type Schemas struct {
	Board       json.RawMessage `json:"board"`
	CLIPack     json.RawMessage `json:"cliPack"`
	BrowserPack json.RawMessage `json:"browserPack"`
}

// LoadSchemas reads the embedded schemas.
func LoadSchemas() (Schemas, error) {
	var s Schemas
	for name, dst := range map[string]*json.RawMessage{"board": &s.Board, "cliPack": &s.CLIPack, "browserPack": &s.BrowserPack} {
		data, err := schemaFS.ReadFile("schema/" + name + ".json")
		if err != nil {
			return s, err
		}
		*dst = data
	}
	return s, nil
}
