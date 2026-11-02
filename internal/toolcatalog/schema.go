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

// botMessageSendInput keeps the common message schema, without group or
// owner-authority options. It is built separately so other roles keep theirs.
var botMessageSendInput = func() json.RawMessage {
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(loadSchema("agent_message_send").Input, &schema); err != nil {
		panic(err)
	}
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(schema["properties"], &properties); err != nil {
		panic(err)
	}
	delete(properties, "to_set")
	delete(properties, "as_owner")
	data, err := json.Marshal(properties)
	if err != nil {
		panic(err)
	}
	schema["properties"] = data
	// A bot must name one recipient; the common schema also allows a group.
	schema["required"] = json.RawMessage(`["body","to"]`)
	data, err = json.Marshal(schema)
	if err != nil {
		panic(err)
	}
	return data
}()
