package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The read must not turn a malformed stored proposal into a failed list.
func TestCTOOwnerItemsFindOpenProposalHasAnswer(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	if _, err := w.d.st.db.Exec(`INSERT INTO cto_proposals(tool,action,args,reason,created_at,status,note,result) VALUES('cto_new','',?,'',?,'open','','')`, `{}`, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	out, err := w.d.runCTOToolRaw(context.Background(), "cto_proposals_find", json.RawMessage(`{"kind":"proposal"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(out)
	for _, want := range []string{`"action":""`, `"reason":""`, `"created":"`, "proposal_ask"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("proposal row lacks %s: %s", want, body)
		}
	}
}
