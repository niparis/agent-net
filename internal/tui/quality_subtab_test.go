package tui

import "testing"

func TestQualityDetailsOpenClose(t *testing.T) {
	m := newModel(120, 34)
	m.tab = quotasTab
	m = keys(m, "tab", "tab")
	if !m.qualityDetail {
		t.Fatal("tabs did not open details")
	}
	m = keys(m, "esc")
	if !m.qualityDetail {
		t.Fatal("esc left Quality sub tab")
	}
}

func TestQuotaSubtabCycle(t *testing.T) {
	m := newModel(120, 34)
	m.tab = quotasTab
	m = keys(m, "tab")
	if !m.workersLog {
		t.Fatal("expected Workers log")
	}
	m = keys(m, "tab")
	if !m.qualityDetail || m.workersLog {
		t.Fatal("expected Quality")
	}
	m = keys(m, "tab")
	if m.qualityDetail || m.workersLog {
		t.Fatal("expected Quotas")
	}
	m = keys(m, "shift+tab")
	if !m.qualityDetail {
		t.Fatal("expected reverse Quality")
	}
	m = keys(m, "shift+tab")
	if !m.workersLog {
		t.Fatal("expected reverse Workers log")
	}
}
