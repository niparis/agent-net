package toolcatalog

import (
	"embed"
	"encoding/json"
)

//go:embed schemas/*.json
var schemaFiles embed.FS

type schemaSet struct {
	Input    json.RawMessage `json:"input"`
	Output   json.RawMessage `json:"output"`
	CTOInput json.RawMessage `json:"cto_input"`
}

func loadSchema(name string) schemaSet {
	data, err := schemaFiles.ReadFile("schemas/" + name + ".json")
	if err != nil {
		panic("toolcatalog: " + err.Error())
	}
	var set schemaSet
	if err := json.Unmarshal(data, &set); err != nil {
		panic("toolcatalog: schema for " + name + ": " + err.Error())
	}
	return set
}

func toolEntry(name, description string, roles []Role, hints Hints, alwaysLoad, hasCTOInput bool) Tool {
	set := loadSchema(name)
	tool := Tool{
		Name:         name,
		Description:  description,
		Roles:        roles,
		Hints:        hints,
		InputSchema:  set.Input,
		OutputSchema: set.Output,
		AlwaysLoad:   alwaysLoad,
	}
	if hasCTOInput {
		tool.CTOInputSchema = set.CTOInput
	}
	return tool
}
