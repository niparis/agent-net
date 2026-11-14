package protocol

import "testing"

func TestRoleModelsReadAndWriteTheSettingLine(t *testing.T) {
	text := "claude · anthropic · opus · high\n\ncodex · openai · gpt-6.1-sol\n"
	got, err := ParseRoleModels(text)
	if err != nil {
		t.Fatal(err)
	}
	want := []RoleModel{
		{Harness: "claude", Provider: "anthropic", Model: "opus", Effort: "high"},
		{Harness: "codex", Provider: "openai", Model: "gpt-6.1-sol"},
	}
	if len(got) != len(want) || !sameRoleEntry(got[0], want[0]) || !sameRoleEntry(got[1], want[1]) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if back := FormatRoleModels(got); back != "claude · anthropic · opus · high\ncodex · openai · gpt-6.1-sol" {
		t.Fatalf("format: %q", back)
	}
}

// sameRoleEntry reports whether two entries name the same model. The
// candidate type carries ranker facts, so it is not comparable with ==.
func sameRoleEntry(a, b RoleModel) bool {
	return a.Harness == b.Harness && a.Provider == b.Provider && a.Model == b.Model && a.Effort == b.Effort
}

func TestRoleModelsRefuseAnEntryWithoutAHarness(t *testing.T) {
	if _, err := ParseRoleModels("· anthropic · opus"); err == nil {
		t.Fatal("accepted an entry with no harness")
	}
	if _, err := ParseRoleModels("claude · anthropic · opus · high · extra"); err == nil {
		t.Fatal("accepted an entry with five parts")
	}
}
