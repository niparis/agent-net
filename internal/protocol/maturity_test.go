package protocol

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMaturityDefaultsTable(t *testing.T) {
	tests := []struct {
		value string
		want  MaturityDefaults
	}{
		{"early - speed over safety", MaturityDefaults{
			Level: MaturityEarly, TestsBeforeMerge: TestsOptional, MergeAuthority: MergeFull,
			WorkerSandbox: WorkersSandboxed, JudgeConsumers: JudgeFree, DeployRule: DeployBrief}},
		{"growing", MaturityDefaults{
			Level: MaturityGrowing, TestsBeforeMerge: TestsSuite, MergeAuthority: MergeFull,
			WorkerSandbox: WorkersSandboxed, JudgeConsumers: JudgeCharter, DeployRule: DeployBrief}},
		{"mature - careful", MaturityDefaults{
			Level: MaturityMature, TestsBeforeMerge: TestsSuiteNewTests, MergeAuthority: MergeDelegated,
			WorkerSandbox: WorkersSandboxed, JudgeConsumers: JudgeStrict, DeployRule: DeployAsk}},
		{"critical", MaturityDefaults{
			Level: MaturityCritical, TestsBeforeMerge: TestsSuiteNewTestsReview, MergeAuthority: MergeAsk,
			WorkerSandbox: WorkersNoNetwork, JudgeConsumers: JudgeEscalate, DeployRule: DeployOwner}},
	}
	for _, tc := range tests {
		if got := DefaultsForMaturity(tc.value); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: %#v, want %#v", tc.value, got, tc.want)
		}
	}
}

func TestMaturityDefaultsOverrides(t *testing.T) {
	d := DefaultsForMaturity("mature").Apply(map[string]string{
		SettingMergeAuthority:   MergeAsk,
		SettingWorkerSandbox:    WorkersReadOnly,
		SettingTestsBeforeMerge: TestsOptional,
		SettingJudgeConsumers:   JudgeFree,
		SettingDeployRule:       DeployOwner,
		"not_a_setting":         "ignored",
	})
	want := MaturityDefaults{Level: MaturityMature, TestsBeforeMerge: TestsOptional, MergeAuthority: MergeAsk,
		WorkerSandbox: WorkersReadOnly, JudgeConsumers: JudgeFree, DeployRule: DeployOwner}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("overrides: %#v, want %#v", d, want)
	}
	// A value that is not valid for its setting is ignored, not applied.
	d = DefaultsForMaturity("early").Apply(map[string]string{SettingWorkerSandbox: "yolo"})
	if d.WorkerSandbox != WorkersSandboxed {
		t.Fatalf("bad override applied: %#v", d)
	}
	if err := ValidateNodeSetting(SettingTestsBeforeMerge, TestsSuite); err != nil {
		t.Fatalf("valid setting refused: %v", err)
	}
	if err := ValidateNodeSetting(SettingMergeAuthority, "sometimes"); err == nil {
		t.Fatal("an invalid value was accepted")
	}
	if err := ValidateNodeSetting("nonsense", "x"); err == nil {
		t.Fatal("an unknown setting was accepted")
	}
}

func TestMaturityRulesText(t *testing.T) {
	got := DefaultsForMaturity("mature").RulesText()
	want := "maturity mature: run the suite and add tests for new behavior; merge needs your owner's or a delegate's approval; ask before deploying"
	if got != want {
		t.Fatalf("rules text:\n got %q\nwant %q", got, want)
	}
	for _, d := range []MaturityDefaults{DefaultsForMaturity("early"), DefaultsForMaturity("growing"), DefaultsForMaturity("critical")} {
		if text := d.RulesText(); text == "" || strings.Contains(text, "\u2014") {
			t.Errorf("%s rules text: %q", d.Level, text)
		}
	}
	if got := DefaultsForMaturity("mature").ConsumerText(); !strings.Contains(got, "strictly") {
		t.Fatalf("consumer text: %q", got)
	}
	if got := DefaultsForMaturity("critical").DeployText(); !strings.Contains(got, "ask the owner") {
		t.Fatalf("deploy text: %q", got)
	}
}

func TestNodeContextStatesRulesAndTheDeployTargetsRule(t *testing.T) {
	g := Graph{
		Nodes: []Node{
			{Name: "homelab", Kind: "component", Charter: Charter{Maturity: "growing - deploys matter."}},
			{Name: "shop", Kind: "component", Charter: Charter{Maturity: "critical - money."}},
		},
		Edges: []Edge{{From: "homelab", To: "shop", Kind: "deploys", Rule: "normal"}},
	}
	got := NodeContext(g, "homelab")
	for _, want := range []string{
		"maturity growing: run the suite; merge and push to the default branch; deploy as the brief says",
		"judge requests from consumers by the charter",
		"You deploy shop; its maturity critical: run the suite, add tests for new behavior and get a review; merge needs your owner's written approval; ask the owner before deploying",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("context lacks %q:\n%s", want, got)
		}
	}
}

func TestCharterBriefStatesTheNodeRules(t *testing.T) {
	n := Node{Name: "cogforge", Kind: "component", Charter: Charter{Maturity: "mature - careful."},
		Settings: map[string]string{SettingMergeAuthority: MergeAsk}}
	got := CharterBrief(n)
	for _, want := range []string{
		"maturity: mature - careful.",
		"maturity mature: run the suite and add tests for new behavior; merge needs your owner's written approval; ask before deploying",
		"judge requests from consumers strictly",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("brief lacks %q:\n%s", want, got)
		}
	}
}

func TestMergeApproval(t *testing.T) {
	g := Graph{
		Nodes: []Node{
			{Name: "cogforge", Kind: "component", Charter: Charter{Maturity: "mature"}},
			{Name: "early-app", Kind: "component", Charter: Charter{Maturity: "early"}},
			{Name: "trading", Kind: "architect"},
			{Name: "ops", Kind: "architect"},
			{Name: "observer", Kind: "architect"},
		},
		Edges: []Edge{
			{From: "trading", To: "cogforge", Kind: "governs", Mode: "owner", Rule: "normal"},
			{From: "ops", To: "cogforge", Kind: "governs", Mode: "arbiter", Rule: "normal"},
			{From: "observer", To: "cogforge", Kind: "governs", Mode: "advisor", Rule: "normal"},
		},
	}
	got := MergeApprovalFor(g, "cogforge")
	if got.Authority != MergeDelegated || !got.Required || got.Written {
		t.Fatalf("delegated approval: %#v", got)
	}
	joined := strings.Join(got.Approvers, ",")
	if !strings.Contains(joined, "trading") || !strings.Contains(joined, "ops") || strings.Contains(joined, "observer") {
		t.Fatalf("delegates: %#v", got.Approvers)
	}
	if !strings.Contains(got.Text, "your owner's or a delegate's approval") {
		t.Fatalf("text: %q", got.Text)
	}
	if got := MergeApprovalFor(g, "early-app"); got.Authority != MergeFull || got.Required {
		t.Fatalf("full approval: %#v", got)
	}
	g.Nodes[0].Settings = map[string]string{SettingMergeAuthority: MergeAsk}
	if got := MergeApprovalFor(g, "cogforge"); got.Authority != MergeAsk || !got.Written || !got.Required {
		t.Fatalf("ask approval: %#v", got)
	}
	if _, err := RequestWrittenApproval(ApprovalRequest{Action: "merge cogforge abc123"}); !errors.Is(err, ErrInboxNotBuilt) {
		t.Fatalf("inbox stub: %v", err)
	}
	note := WrittenApprovalNote("Nicolas", "merge cogforge abc123", time.Date(2026, 10, 30, 14, 2, 0, 0, time.UTC))
	if note != "Nicolas approved: merge cogforge abc123, 14:02" {
		t.Fatalf("note: %q", note)
	}
}
