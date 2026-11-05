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
