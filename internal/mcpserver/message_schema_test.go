package mcpserver

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestMessageOutputSchemasCoverOperatorFields(t *testing.T) {
	for name, value := range map[string]any{
		"agent_messages_find": roMessagesFindOut{Items: []roMessageRow{{DeliveryDetail: "waiting for recipient"}}},
		"agent_message_get":   roMessageGetOut{Message: roMessageDetail{DeliveryDetail: "waiting for recipient"}},
	} {
		t.Run(name, func(t *testing.T) {
			spec := roCatalogTool(t, name)
			var schema jsonschema.Schema
			if err := json.Unmarshal(spec.OutputSchema, &schema); err != nil {
				t.Fatal(err)
			}
			resolved, err := schema.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var instance any
			if err := json.Unmarshal(raw, &instance); err != nil {
				t.Fatal(err)
			}
			if err := resolved.Validate(instance); err != nil {
				t.Errorf("output validation: %v", err)
			}

			var document struct {
				Defs map[string]struct{ Properties map[string]json.RawMessage } `json:"$defs"`
			}
			if err := json.Unmarshal(spec.OutputSchema, &document); err != nil {
				t.Fatal(err)
			}
			typ := reflect.TypeOf(roMessageDetail{})
			if name == "agent_messages_find" {
				typ = reflect.TypeOf(roMessageRow{})
			}
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				if !field.IsExported() {
					continue
				}
				key := strings.Split(field.Tag.Get("json"), ",")[0]
				if key == "-" {
					continue
				}
				if _, ok := document.Defs["Message"].Properties[key]; !ok {
					t.Errorf("Message schema missing operator field %s", key)
				}
			}
		})
	}
}
