package protocol

import "testing"

func TestSameAskTextKeepsDifferentIdsApart(t *testing.T) {
	cases := []struct {
		a, b string
		same bool
	}{
		{"Merge PR #3?", " merge   pr #3? ", true},
		{"Merge #935 at 2143e3af", "Merge #935 at 2143e3af", true},
		{"Merge #935 at 2143e3af", "Merge #935 at 9999abcd", false},
		{"Merge #935 at 2143e3af", "Merge #935", false},
		{"Ship the release?", "Ship the release today?", false},
	}
	for _, c := range cases {
		if got := SameAskText(c.a, c.b); got != c.same {
			t.Errorf("SameAskText(%q, %q) = %v, want %v", c.a, c.b, got, c.same)
		}
	}
}

func TestAskWithdrawalReadsTheAskAndReason(t *testing.T) {
	cases := []struct {
		text, id, reason string
		ok               bool
	}{
		{"Withdraw i_255e3e1156901746", "i_255e3e1156901746", "", true},
		{"withdraw i_255e3e1156901746: the head moved", "i_255e3e1156901746", "the head moved", true},
		{"Withdraw i_255e3e1156901746\nThe head moved, a newer approval replaces it.", "i_255e3e1156901746", "The head moved, a newer approval replaces it.", true},
		{"Owner question: withdraw i_255e3e1156901746", "", "", false},
		{"Withdraw my ask", "", "", false},
	}
	for _, c := range cases {
		id, reason, ok := AskWithdrawal(c.text)
		if ok != c.ok || id != c.id || reason != c.reason {
			t.Errorf("AskWithdrawal(%q) = %q, %q, %v; want %q, %q, %v", c.text, id, reason, ok, c.id, c.reason, c.ok)
		}
	}
}

func TestAskSortWithdrawTakesAReplacement(t *testing.T) {
	ok := AskSortArgs{Ask: "i_1", Result: AskSortWithdraw, Reason: "the head moved", ReplacedBy: "i_2"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("withdraw with a replacement: %v", err)
	}
	if err := (&AskSortArgs{Ask: "i_1", Result: AskSortWithdraw, Reason: "the head moved"}).Validate(); err != nil {
		t.Fatalf("withdraw without a replacement: %v", err)
	}
	if err := (&AskSortArgs{Ask: "i_1", Result: AskSortWithdraw, Reason: "the head moved", ReplacedBy: "i_1"}).Validate(); err == nil {
		t.Fatal("withdraw named the ask itself as its replacement")
	}
	if err := (&AskSortArgs{Ask: "i_1", Result: AskSortClose, Reason: "done", ReplacedBy: "i_2"}).Validate(); err == nil {
		t.Fatal("close accepted replaced_by")
	}
}
