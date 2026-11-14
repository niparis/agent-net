package protocol

import (
	"reflect"
	"testing"
)

func TestCharterSetAcceptsCriticalMaturity(t *testing.T) {
	got, err := CleanCharterValue("maturity", MaturityCritical)
	if err != nil || got != MaturityCritical {
		t.Fatalf("charter_set maturity critical = %q, %v", got, err)
	}
}

func TestCharterMaturityEnum(t *testing.T) {
	for _, v := range MaturityLevels {
		if _, err := CleanCharterValue("maturity", v); err != nil {
			t.Fatal(v, err)
		}
	}
	for _, v := range []string{"unknown", "critcal", "mature - daily use"} {
		if _, err := CleanCharterValue("maturity", v); err == nil {
			t.Fatalf("accepted %q", v)
		}
	}
	for _, attr := range []string{"consumers", "needs", "notes"} {
		if _, err := CleanCharterValue(attr, "x"); err == nil {
			t.Fatalf("accepted %s", attr)
		}
	}
	for _, attr := range []string{"rules", "maturity_note"} {
		if _, err := CleanCharterValue(attr, "house style"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCharterEnumPipeline(t *testing.T) {
	for _, level := range []string{"", "experimental", "early", "growing", "mature"} {
		p, err := PipelineForMaturity(level)
		if err != nil {
			t.Fatal(err)
		}
		legacy := level
		if level == "experimental" {
			legacy = "early"
		}
		want, err := PipelineForMaturity(legacy)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(p, want) {
			t.Fatalf("pipeline for %s changed", level)
		}
	}
}
