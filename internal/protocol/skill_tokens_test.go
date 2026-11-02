package protocol

import "testing"

func TestSkillTokenEstimates(t *testing.T) {
	s, f := SkillTokenEstimates("demo", "abc", &SkillManifest{Files: []SkillFile{{Path: "SKILL.md", Size: 9}}})
	if s != 7 || f != 3 {
		t.Fatalf("%d %d", s, f)
	}
	_, f = SkillTokenEstimates("", "", nil)
	if f != 0 {
		t.Fatal(f)
	}
}
