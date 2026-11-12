package protocol

import "testing"

// Only a line marked "Owner question:" is a question to the owner; a plain
// turn end, or a question without the mark, is not.
func TestOwnerQuestionLine(t *testing.T) {
	for _, tc := range []struct{ msg, want string }{
		{"Done. Tests pass.", ""},
		{"Should I also update the docs?", ""},
		{"Done.\nOwner question: ship v1.2 today?", "ship v1.2 today?"},
		{"**Owner question:** ship v1.2 today?\n", "ship v1.2 today?"},
		{"- owner question: which region?", "which region?"},
		{"Owner question:\nmore text", ""},
		{"Owner question: first?\nnotes\nOwner question: second?", "second?"},
	} {
		if got := OwnerQuestionLine(tc.msg); got != tc.want {
			t.Errorf("OwnerQuestionLine(%q) = %q, want %q", tc.msg, got, tc.want)
		}
	}
}
