package protocol

import (
	"strings"
	"testing"
)

func TestAskIntakeArgsValidate(t *testing.T) {
	ok := []AskIntakeArgs{
		{Action: AskIntakeList},
		{Action: AskIntakeList, Limit: 50},
		{Action: AskIntakeRead, Session: "s_1"},
		{Action: AskIntakeImport, Session: "s_1", Turn: AskIntakeSourceRef("s_1", "7:1"), Text: "Rotate it?", Options: []string{"yes", "no"}, Recommendation: "Rotate it."},
	}
	for _, a := range ok {
		if err := a.Validate(); err != nil {
			t.Errorf("%+v: %v", a, err)
		}
	}
	bad := []AskIntakeArgs{
		{},
		{Action: "sort"},
		{Action: AskIntakeList, Session: "s_1"},
		{Action: AskIntakeList, Limit: 51},
		{Action: AskIntakeRead},
		{Action: AskIntakeRead, Session: "s_1", Text: "x"},
		{Action: AskIntakeImport, Turn: "x", Text: "q"},
		{Action: AskIntakeImport, Session: "s_1", Text: "q"},
		{Action: AskIntakeImport, Session: "s_1", Turn: "x"},
		{Action: AskIntakeImport, Session: "s_1", Turn: "x", Text: "one\ntwo"},
		{Action: AskIntakeImport, Session: "s_1", Turn: "x", Text: "q", Options: []string{"1", "2", "3", "4", "5"}},
	}
	for _, a := range bad {
		if err := a.Validate(); err == nil {
			t.Errorf("%+v was accepted", a)
		}
	}
	if got := AskIntakeSourceRef("s_1", "7:1"); got != "ask-intake:s_1:7:1" || !strings.HasPrefix(got, AskIntakeSourceRefPrefix) {
		t.Fatalf("source ref = %q", got)
	}
}
