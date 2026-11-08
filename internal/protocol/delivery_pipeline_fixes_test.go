package protocol

import "testing"

func TestValidFixes(t *testing.T) {
	for _, tc := range []struct {
		change   string
		severity int
		ok       bool
	}{
		{"", 0, true},
		{"", 2, false},
		{"ch_abc", 0, true},
		{"ch_abc", 1, true},
		{"ch_abc", 4, true},
		{"ch_abc", 5, false},
		{"ch_abc", -1, false},
		{"abc", 0, false},
		{"ch_", 0, false},
	} {
		err := ValidFixes(tc.change, tc.severity)
		if (err == nil) != tc.ok {
			t.Errorf("ValidFixes(%q, %d) = %v, want ok %v", tc.change, tc.severity, err, tc.ok)
		}
	}
}

func TestEvidenceFlakeAndSeverityFieldsAreChecked(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		ok          bool
	}{
		{FieldFlake, "yes", true}, {FieldFlake, "no", true}, {FieldFlake, "maybe", false},
		{FieldSeverity, "1", true}, {FieldSeverity, "4", true}, {FieldSeverity, "0", false}, {FieldSeverity, "5", false}, {FieldSeverity, "high", false},
	} {
		err := validFields(map[string]string{tc.name: tc.value})
		if (err == nil) != tc.ok {
			t.Errorf("field %s=%s: %v, want ok %v", tc.name, tc.value, err, tc.ok)
		}
	}
}

func TestChangeSubmitRequestChecksFixes(t *testing.T) {
	q := ChangeSubmitRequest{SessionID: "s_1", Project: "demo", Branch: "b", HeadCommit: commitA, Origin: ChangeOrigin{Kind: OriginSession, SessionID: "s_1"}, Fixes: "nope"}
	if err := q.Validate(); err == nil {
		t.Fatal("a fixes that is not a change ID was accepted")
	}
}
