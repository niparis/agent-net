package protocol

import "strings"

// CTOOperatorReads and CTOOperatorChanges are the owner operations exposed by
// settings_change area operator. The private CTO process check is still required.
// Consent completion and UI transport acknowledgements are deliberately absent.
const (
	OpWorkerFloorsReset = "worker_floors_reset"
	OpOperatorUndo      = "operator_undo"
	OpMergerGet         = "merger_get"
	OpMergerRestore     = "merger_restore"
	OpAppsPublicRestore = "apps.public.restore"
)

const CTOOperatorReads = `info nodes list projects events message_status workers project_display_get sessions_archived worker_log apps.pending inventory_get inventory_scan
mcp_import_candidates tools_items tools_item tools_projects tools_history tools_accepted tools_needs
 tools_read_file tools_summary tools_skill_details tools_versions tools_differences tools_update_plan
merger_get session_start_get inbox_list asks_list chief_get chief_sorts ask_history desk_log outcome_inbox learned_list learned_view
usage usage_top session_text counts conversations conversation_messages graph_view graph_talk
session_agent_list integrations_get hidden_get onboarding_get quality_get timezone_get
protected_providers_get worker_floors_get worker_providers_get difficulty_get scores_get scores_weekly
worker_rotation_get difficulty_judge_pick cto_events cto_graph_history held_list team_history
graph_get charter_history edge_history node_settings_get settings_owner_read changes_owner_find playbook_get picker_get
sign_in_list nudge_stats`

const CTOOperatorChanges = `operator_undo sessions_archive cto_enter cto_send session_enter apps.public.restore session_pin apps.refuse apps.public.url tools_keep_copy tools_save_changed tools_update_copies tools_import_folder
mcp_coverage_plan mcp_coverage_apply mcp_coverage_undo tools_accept tools_import tools_export gateway_backup
session_stop session_resume session_replace session_agent_set learned_answer hidden_set onboarding_set quality_set timezone_set
worker_floors_set worker_floors_reset difficulty_set team_write delivery_owner_set merger_set merger_restore chief_set ask_undo ask_mark changes_owner_report worker_defect sign_in_raise`

// CTOOperatorMethods is an explicit allowlist, shared by schema generation and
// daemon dispatch. It never accepts arbitrary daemon methods or consent approval.
func CTOOperatorMethods() map[string]bool {
	out := map[string]bool{}
	for _, name := range strings.Fields(CTOOperatorReads) {
		out[name] = true
	}
	for _, name := range strings.Fields(CTOOperatorChanges) {
		out[name] = false
	}
	return out
}
