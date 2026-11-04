package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

const testSum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestValidateFilesLimits(t *testing.T) {
	ok := FileEntry{ID: "f1", Name: "report file.html", Size: 10, SHA256: testSum}
	if err := ValidateFiles([]FileEntry{ok}); err != nil {
		t.Fatalf("a valid manifest was refused: %v", err)
	}
	if err := ValidateFiles(nil); err != nil {
		t.Fatalf("an empty manifest was refused: %v", err)
	}
	many := make([]FileEntry, MaxFilesPerMessage+1)
	for i := range many {
		many[i] = FileEntry{ID: "f" + string(rune('a'+i)), Name: "n" + string(rune('a'+i)), SHA256: testSum}
	}
	bad := map[string][]FileEntry{
		"eleven files":     many,
		"too large":        {{ID: "f1", Name: "a", Size: MaxFileBytes + 1, SHA256: testSum}},
		"negative size":    {{ID: "f1", Name: "a", Size: -1, SHA256: testSum}},
		"slash in name":    {{ID: "f1", Name: "../a", SHA256: testSum}},
		"backslash":        {{ID: "f1", Name: `a\b`, SHA256: testSum}},
		"dot dot":          {{ID: "f1", Name: "..", SHA256: testSum}},
		"control char":     {{ID: "f1", Name: "a\nb", SHA256: testSum}},
		"empty name":       {{ID: "f1", Name: "", SHA256: testSum}},
		"long name":        {{ID: "f1", Name: strings.Repeat("a", MaxFileNameBytes+1), SHA256: testSum}},
		"upper-case hash":  {{ID: "f1", Name: "a", SHA256: strings.ToUpper(testSum)}},
		"short hash":       {{ID: "f1", Name: "a", SHA256: "abc"}},
		"bad id":           {{ID: "F-1", Name: "a", SHA256: testSum}},
		"duplicate id":     {ok, {ID: "f1", Name: "b", SHA256: testSum}},
		"duplicate name":   {ok, {ID: "f2", Name: ok.Name, SHA256: testSum}},
		"space at the end": {{ID: "f1", Name: "a ", SHA256: testSum}},
	}
	for what, files := range bad {
		if err := ValidateFiles(files); err == nil {
			t.Errorf("%s: accepted", what)
		}
	}
}

func TestTaildropName(t *testing.T) {
	f := FileEntry{ID: "f2", Name: "my report.html"}
	if got, want := TaildropName("m_0011", f), "agent-net-m_0011-f2-my report.html"; got != want {
		t.Fatalf("TaildropName = %q, want %q", got, want)
	}
	if !strings.HasPrefix(TaildropName("m_0011", f), TaildropPrefix("m_0011")) {
		t.Fatal("the unique name does not start with the message prefix")
	}
	// The longest name still fits the 255-byte limit of a file name.
	long := FileEntry{ID: "f10", Name: strings.Repeat("a", MaxFileNameBytes)}
	if n := len(TaildropName(NewID("m"), long)); n > 255 {
		t.Fatalf("the longest unique name is %d bytes", n)
	}
}

func TestMessageCarriesFiles(t *testing.T) {
	m := Message{ID: "m_1", Files: []FileEntry{{ID: "f1", Name: "a.txt", Size: 3, SHA256: testSum}}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var back Message
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Files) != 1 || back.Files[0] != m.Files[0] {
		t.Fatalf("files lost in JSON: %s", raw)
	}
	// A message without files keeps its old JSON shape.
	plain, _ := json.Marshal(Message{ID: "m_2"})
	if strings.Contains(string(plain), `"files"`) {
		t.Fatalf("a message without files has a files key: %s", plain)
	}
	send, _ := json.Marshal(MsgSend{Files: m.Files})
	if !strings.Contains(string(send), `"files":[`) {
		t.Fatalf("msg.send drops the files: %s", send)
	}
}
