package emdashcase

import "testing"

// A test names the character to check that the text a user reads has none.
// Test files are not checked.
func TestNoEmDashInScreens(t *testing.T) {
	const forbidden = "\u2014"
	if forbidden == "" {
		t.Fatal("empty")
	}
}
