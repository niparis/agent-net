package protocol

import (
	"reflect"
	"strings"
	"testing"
)

// requestParams are the structs the daemon and the gateway send as request
// parameters. Each one must survive EncodeRequestVia: a top-level id or type
// would be silently replaced by the frame's own field.
//
// This is the regression test for nudge.resume and nudge.undo, which shipped
// with an id parameter and failed on every call.
var requestParams = []any{
	NudgePauseRequest{},
	NudgeResumeRequest{},
	NudgeUndoRequest{},
	NudgePausesFindRequest{},
	ChangeSubmitRequest{},
	ChangeGetRequest{},
	ChangeListRequest{},
	ChangeReconcileRequest{},
	StepClaimRequest{},
	StepReportRequest{},
	DeliveryNeedsPushRequest{},
	AskSortRequest{},
	AskIntakeRequest{},
	OwnerBriefRequest{},
	RecentWorkerChangesRequest{},
	PolicyGet{},
	QueryAgents{},
	QueryAgent{},
	QueryProjects{},
	QueryMessage{},
	QueryConversations{},
	QueryTeamLead{},
	WorkersWatch{},
	ToolsFilter{},
	MCPCall{},
	MCPCatalog{},
	MCPSync{},
	OwnerSendRequest{},
	WorkerStartRequest{},
	WorkerStopRequest{},
	WorkerHandbackRequest{},
	SessionTrustRequest{},
	SessionRenameRequest{},
	NodeRenameRequest{},
	ProjectDisplayRequest{},
	SessionAgentSetRequest{},
	SettingsWrite{},
	SettingsUndo{},
	DeskLogQuery{},
	GraphRead{},
	GraphHistoryRequest{},
	GraphLifecycleRequest{},
	GraphNodeRequest{},
	GraphUndoFrame{},
	CharterSetRequest{},
	CharterDraftFrame{},
	LearnFrame{},
	RateFrame{},
	PermissionProposeFrame{},
	PermissionTrustAsk{},
	PermissionApplyFrame{},
	PermissionUndoFrame{},
	MsgSend{},
	MsgWithdraw{},
	MsgHandoff{},
	SessionUpsert{},
	SessionSync{},
	SessionRemove{},
	SessionRelay{},
	SessionFolderTrustRelay{},
	SessionFolderMCPRelay{},
	SessionProcessStopRelay{},
	EnterRequest{},
	ProcessStopRequest{},
	FolderMCPRequest{},
	ProjectAddRequest{},
	ProjectsLayoutRequest{},
	FileSendRequest{},
	WorkerLaunch{},
	WorkerContinue{},
	WorkerResume{},
	WorkerKill{},
	WorkerStopped{},
	WorkerRunState{},
	WorkerCleanup{},
	WorkerProfileFrame{},
	TransferBegin{},
	TransferStatus{},
	TransferPart{},
	TransferFinish{},
	TransferGet{},
	TransferConfirm{},
	NodeUpgrade{},
	NodeName{},
	NodeHarnesses{},
	PlaybookGetRequest{},
	MCPItemsChanged{},
	DeliveryUpdate{},
	ToolVersionRequest{},
	SkillBundleRequest{},
	HintRequest{},
	MachineLogRequest{},
	ScoreSetRequest{},
	RateRequest{},
	DefectRequest{},
	LearnRequest{},
	ApprovalRequest{},
	NodeSettingsGetRequest{},
	NodeSettingsSetRequest{},
	CTOProposalAnswer{},
	DifficultyJudgePick{},
	SessionStartFailure{},
	SessionAgentReservation{},
}

// TestRequestParamsAvoidFrameFields encodes every request parameter struct
// through the same function the daemon link uses. It fails on a top-level id
// or type, even one hidden behind omitempty, and on any struct that no longer
// encodes.
func TestRequestParamsAvoidFrameFields(t *testing.T) {
	for _, params := range requestParams {
		rt := reflect.TypeOf(params)
		checkFrameFields(t, rt)
		if _, err := EncodeRequestVia("test.frame", "1", ViaTUI, params); err != nil {
			t.Errorf("%s: %v", rt, err)
		}
	}
}

// checkFrameFields walks the top-level JSON names, including anonymous
// embedded structs, which end up next to type and id in the frame.
func checkFrameFields(t *testing.T, rt reflect.Type) {
	t.Helper()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.Anonymous {
			checkFrameFields(t, f.Type)
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "-" || name == "" {
			continue
		}
		if name == "id" || name == "type" {
			t.Errorf("%s has a top-level parameter named %q", rt, name)
		}
	}
}
