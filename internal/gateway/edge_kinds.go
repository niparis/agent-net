package gateway

import "strings"

// Edge endpoint kinds describe the existing keys without changing their meaning.
func edgeKindsMigration() string {
	const schema = `
-- Typed edge metadata; legacy endpoint keys remain unchanged.
UPDATE graph_edges SET
 source_kind=CASE WHEN kind='depends' THEN 'repo' WHEN kind='governs' THEN 'agent' WHEN source LIKE '/delivery/%' THEN 'special' ELSE 'agent' END,
 target_kind=CASE WHEN kind='governs' THEN 'agent' ELSE 'repo' END;
UPDATE graph_history SET
 source_kind=CASE WHEN attr='depends' THEN 'repo' WHEN attr='governs' THEN 'agent' WHEN node LIKE '/delivery/%' THEN 'special' ELSE 'agent' END,
 target_kind=CASE WHEN attr='governs' THEN 'agent' ELSE 'repo' END WHERE type='edge';
CREATE TRIGGER IF NOT EXISTS edge_kinds_insert AFTER INSERT ON graph_edges BEGIN
 UPDATE graph_edges SET
 source_kind=CASE WHEN NEW.kind='depends' THEN 'repo' WHEN NEW.kind='governs' THEN 'agent' WHEN NEW.source LIKE '/delivery/%' THEN 'special' ELSE 'agent' END,
 target_kind=CASE WHEN NEW.kind='governs' THEN 'agent' ELSE 'repo' END
 WHERE source=NEW.source AND target=NEW.target AND kind=NEW.kind;
END;
CREATE TRIGGER IF NOT EXISTS edge_history_kinds_insert AFTER INSERT ON graph_history WHEN NEW.type='edge' BEGIN
 UPDATE graph_history SET
 source_kind=CASE WHEN NEW.attr='depends' THEN 'repo' WHEN NEW.attr='governs' THEN 'agent' WHEN NEW.node LIKE '/delivery/%' THEN 'special' ELSE 'agent' END,
 target_kind=CASE WHEN NEW.attr='governs' THEN 'agent' ELSE 'repo' END WHERE id=NEW.id;
END;
`
	// The shipped trigger predicates and task routing stay intact; only its
	// inserted caller edge gains explicit metadata.
	trigger := strings.Replace(mergerLifecycleMigration(),
		"INSERT OR IGNORE INTO graph_edges(source,target,kind,mode,rule)\n SELECT '/delivery/caller',target,kind,mode,rule FROM graph_edges",
		"INSERT OR IGNORE INTO graph_edges(source,target,kind,mode,rule,source_kind,target_kind)\n SELECT '/delivery/caller',target,kind,mode,rule,'special','repo' FROM graph_edges", 1)
	return schema + trigger
}

// orgTreeMarker names the org tree migration (decision 0160).
const orgTreeMarker = "Org tree (decision 0160)"

// orgTreeMigration teaches the edge kind triggers the reports_to kind: an
// Agent to an Agent, or to the owner root, which is special. The columns and
// the tree itself are built in Go (buildOrgTreeOnceTx), so a replay is safe.
const orgTreeMigration = `
-- ` + orgTreeMarker + `: reports_to edges, agents.lead_session, sessions.started_by.
DROP TRIGGER IF EXISTS edge_kinds_insert;
DROP TRIGGER IF EXISTS edge_history_kinds_insert;
CREATE TRIGGER edge_kinds_insert AFTER INSERT ON graph_edges BEGIN
 UPDATE graph_edges SET
 source_kind=CASE WHEN NEW.kind='depends' THEN 'repo' WHEN NEW.kind IN ('governs','reports_to') THEN 'agent' WHEN NEW.source LIKE '/delivery/%' THEN 'special' ELSE 'agent' END,
 target_kind=CASE WHEN NEW.kind='governs' THEN 'agent' WHEN NEW.kind='reports_to' AND NEW.target='owner' THEN 'special' WHEN NEW.kind='reports_to' THEN 'agent' ELSE 'repo' END
 WHERE source=NEW.source AND target=NEW.target AND kind=NEW.kind;
END;
CREATE TRIGGER edge_history_kinds_insert AFTER INSERT ON graph_history WHEN NEW.type='edge' BEGIN
 UPDATE graph_history SET
 source_kind=CASE WHEN NEW.attr='depends' THEN 'repo' WHEN NEW.attr IN ('governs','reports_to') THEN 'agent' WHEN NEW.node LIKE '/delivery/%' THEN 'special' ELSE 'agent' END,
 target_kind=CASE WHEN NEW.attr='governs' THEN 'agent'
  WHEN NEW.attr='reports_to' AND json_extract(CASE WHEN json_valid(NEW.new_value) THEN NEW.new_value ELSE NEW.old_value END,'$.to')='owner' THEN 'special'
  WHEN NEW.attr='reports_to' THEN 'agent' ELSE 'repo' END WHERE id=NEW.id;
END;
`
