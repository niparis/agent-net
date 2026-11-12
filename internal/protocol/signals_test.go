package protocol

import "testing"

func TestQualitySignalValidate(t *testing.T) {
	good := QualitySignal{Code: SignalHeavyTests, Strength: SignalWeak, Evidence: "heavy tests: 151 test lines added for 10 code lines"}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid signal refused: %v", err)
	}
	if got := good.Line(); got != "weak: heavy tests: 151 test lines added for 10 code lines" {
		t.Fatalf("line %q", got)
	}
	for _, bad := range []QualitySignal{
		{Code: "", Strength: SignalWeak, Evidence: "x"},
		{Code: "has space", Strength: SignalWeak, Evidence: "x"},
		{Code: SignalHeavyTests, Strength: "certain", Evidence: "x"},
		{Code: SignalHeavyTests, Strength: SignalWeak, Evidence: ""},
		{Code: SignalHeavyTests, Strength: SignalWeak, Evidence: "line\nbreak"},
	} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("bad signal accepted: %+v", bad)
		}
	}
}

func TestChangeSubmitRejectsTooManySignals(t *testing.T) {
	q := ChangeSubmitRequest{SessionID: "s_1", Project: "demo", Branch: "b", HeadCommit: commitA, Origin: ChangeOrigin{Kind: OriginSession, SessionID: "s_1"}}
	for i := 0; i <= MaxQualitySignals; i++ {
		q.Signals = append(q.Signals, QualitySignal{Code: SignalHeavyTests, Strength: SignalWeak, Evidence: "x"})
	}
	if err := q.Validate(); err == nil {
		t.Fatal("submit with too many signals accepted")
	}
}
