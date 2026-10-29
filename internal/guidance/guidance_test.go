package guidance

import (
	"strings"
	"testing"
)

func TestVariantsDifferOnlyInReplyLine(t *testing.T) {
	c, o := strings.Split(Block(Claude), "\n"), strings.Split(Block(Other), "\n")
	if len(c) != len(o) {
		t.Fatal("different line counts")
	}
	diff := 0
	for i := range c {
		if c[i] != o[i] {
			diff++
			if !strings.Contains(c[i], "SendMessage") || strings.Contains(o[i], "SendMessage") {
				t.Errorf("unexpected difference: %q vs %q", c[i], o[i])
			}
		}
	}
	if diff != 1 {
		t.Fatalf("%d lines differ, want 1", diff)
	}
	if strings.Contains(Block(Claude), "{{") || strings.Contains(Block(Claude), "\xe2\x80\x94") {
		t.Error("placeholder or em dash left in block")
	}
}

// Announcing the task is a rule, not a hint, for every variant.
func TestAnnounceRuleIsMandatory(t *testing.T) {
	for _, v := range []string{Claude, Other} {
		b := Block(v)
		for _, want := range []string{
			"Announce your task. This is required.",
			"At the start of every session, before other work, call `agent_announce` with your current task in one short line.",
			"Call it again whenever your task changes.",
			"AGENTS.md automatically",
			"Do not pass a name unless I ask you to rename the session.",
		} {
			if !strings.Contains(b, want) {
				t.Errorf("%s block lacks %q", v, want)
			}
		}
		if strings.Contains(b, "once you know what you are working on") {
			t.Errorf("%s block still has the old optional wording", v)
		}
	}
}

func TestApplyInsertAndRerun(t *testing.T) {
	out, changed := Apply("# Mine\n\n- rule\n", Claude)
	if !changed || !strings.HasPrefix(out, "# Mine\n\n- rule\n\n"+Begin+"\n## agent-net") || !strings.HasSuffix(out, End+"\n") {
		t.Fatalf("bad insert:\n%s", out)
	}
	again, changed := Apply(out, Claude)
	if changed || again != out {
		t.Fatal("rerun was not idempotent")
	}
	out2, _ := Apply("", Other)
	if out2 != Block(Other) {
		t.Fatal("insert into empty file")
	}
	out3, _ := Apply("no newline", Other)
	if !strings.HasPrefix(out3, "no newline\n\n"+Begin) {
		t.Fatalf("no trailing newline: %q", out3)
	}
}

func TestApplyReplacesMarkedBlock(t *testing.T) {
	old := "top\n\n" + Begin + "\nOLD TEXT\n" + End + "\n\n## Later\n\nkeep\n"
	out, changed := Apply(old, Other)
	if !changed || strings.Contains(out, "OLD TEXT") {
		t.Fatal("block not replaced")
	}
	if !strings.HasPrefix(out, "top\n\n"+Begin) || !strings.HasSuffix(out, End+"\n\n## Later\n\nkeep\n") {
		t.Fatalf("surroundings changed:\n%s", out)
	}
}

func TestApplyMigratesLegacySection(t *testing.T) {
	legacy := strings.TrimRight(strings.ReplaceAll(text, "{{reply}}", "with `agent_reply` and the message ID"), "\n") + "\n"
	// Section at end of file.
	in := "# Global\n\n- a\n\n" + legacy
	out, changed := Apply(in, Other)
	if !changed || out != "# Global\n\n- a\n\n"+Block(Other) {
		t.Fatalf("EOF migration:\n%s", out)
	}
	if out2, ch := Apply(out, Other); ch || out2 != out {
		t.Fatal("not idempotent after migration")
	}
	// Section in the middle.
	in = "# Global\n\n" + legacy + "\n## Next\n\nstuff\n"
	out, _ = Apply(in, Other)
	if out != "# Global\n\n"+Block(Other)+"\n## Next\n\nstuff\n" {
		t.Fatalf("mid migration:\n%s", out)
	}
	if strings.Count(out, "agent-net: working with other agents") != 1 {
		t.Fatal("heading duplicated")
	}
}

func TestRemove(t *testing.T) {
	in := "top\n\n" + Block(Claude) + "\n## Later\n\nkeep\n"
	out, changed := Remove(in)
	if !changed || out != "top\n\n## Later\n\nkeep\n" {
		t.Fatalf("remove mid:\n%q", out)
	}
	out, _ = Remove("top\n\n" + Block(Claude))
	if out != "top\n" {
		t.Fatalf("remove at end: %q", out)
	}
	out, _ = Remove(Block(Claude))
	if out != "" {
		t.Fatalf("remove only block: %q", out)
	}
	if _, changed := Remove("nothing here\n"); changed {
		t.Fatal("changed a file without a block")
	}
	// Apply then Remove restores the original.
	orig := "# Mine\n\n- rule\n"
	added, _ := Apply(orig, Claude)
	if back, _ := Remove(added); back != orig {
		t.Fatalf("round trip: %q", back)
	}
}
