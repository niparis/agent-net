package toolcatalog

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/niparis/agent-net/internal/protocol"
)

// protocolOutputs maps each tool whose output is a protocol type to that type.
// The MCP layer validates every structured result against the tool's output
// schema, so the schema must name every field the type can marshal.
var protocolOutputs = map[string]reflect.Type{
	"agent_nudge_pause":           reflect.TypeOf(protocol.NudgeResult{}),
	"agent_nudge_resume":          reflect.TypeOf(protocol.NudgeResult{}),
	"agent_nudge_pauses_find":     reflect.TypeOf(protocol.NudgeResult{}),
	"agent_machine_log":           reflect.TypeOf(protocol.MachineLogResult{}),
	"agent_change_submit":         reflect.TypeOf(protocol.ChangeSubmitResult{}),
	"agent_step_claim":            reflect.TypeOf(protocol.StepClaimResult{}),
	"agent_step_report":           reflect.TypeOf(protocol.StepReportReply{}),
	"agent_changes_find":          reflect.TypeOf(protocol.ChangesFindResult{}),
	"agent_owner_judgment_record": reflect.TypeOf(protocol.JudgmentRecordResult{}),
	"agent_note_write":            reflect.TypeOf(protocol.NoteWriteResult{}),
	"agent_notes_find":            reflect.TypeOf(protocol.NotesFindResult{}),
}

// TestOutputSchemasCoverProtocolTypes fills every field of each tool's result
// type and validates the JSON against the tool's output schema, using the same
// validator the MCP layer uses. A field added to the Go type without a
// matching schema property fails here, before it fails a live tool call.
func TestOutputSchemasCoverProtocolTypes(t *testing.T) {
	for name, typ := range protocolOutputs {
		t.Run(name, func(t *testing.T) {
			tool, ok := toolNamed(name)
			if !ok {
				t.Fatalf("catalog has no %s", name)
			}
			var schema jsonschema.Schema
			if err := json.Unmarshal(tool.OutputSchema, &schema); err != nil {
				t.Fatalf("output schema: %v", err)
			}
			resolved, err := schema.Resolve(nil)
			if err != nil {
				t.Fatalf("output schema resolve: %v", err)
			}
			value := reflect.New(typ).Elem()
			fillAll(t, value)
			raw, err := json.Marshal(value.Interface())
			if err != nil {
				t.Fatalf("marshal %s: %v", typ, err)
			}
			var instance any
			if err := json.Unmarshal(raw, &instance); err != nil {
				t.Fatalf("decode %s: %v", typ, err)
			}
			if err := resolved.Validate(instance); err != nil {
				t.Fatalf("%s output does not match the catalog schema: %v\nvalue: %s", name, err, raw)
			}
		})
	}
}

func toolNamed(name string) (Tool, bool) {
	for _, tool := range Tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return Tool{}, false
}

// fillAll sets every field of v, recursively, to a non-zero value. Pointers,
// slices and maps get one filled element, so no omitempty field stays out of
// the marshaled JSON.
func fillAll(t *testing.T, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fillAll(t, v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if field := v.Field(i); field.CanSet() {
				fillAll(t, field)
			}
		}
	case reflect.Slice:
		slice := reflect.MakeSlice(v.Type(), 1, 1)
		fillAll(t, slice.Index(0))
		v.Set(slice)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		mk := reflect.New(v.Type().Key()).Elem()
		fillAll(t, mk)
		mv := reflect.New(v.Type().Elem()).Elem()
		fillAll(t, mv)
		m.SetMapIndex(mk, mv)
		v.Set(m)
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	default:
		t.Fatalf("fillAll: cannot fill %s", v.Type())
	}
}
