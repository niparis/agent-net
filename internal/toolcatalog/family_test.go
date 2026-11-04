package toolcatalog

import (
	"encoding/json"
	"testing"
)

func TestWorkerStartNotFamilySchema(t *testing.T) {
	for _, tool := range Tools {
		if tool.Name == "agent_worker_start" {
			var schema struct {
				Properties map[string]struct {
					Type  string `json:"type"`
					Items struct {
						Type string `json:"type"`
					} `json:"items"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
				t.Fatal(err)
			}
			p := schema.Properties["not_family"]
			if p.Type != "array" || p.Items.Type != "string" {
				t.Fatalf("not_family schema: %+v", p)
			}
			return
		}
	}
	t.Fatal("missing tool")
}
