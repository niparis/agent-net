package daemon

import (
	"encoding/json"

	"github.com/niparis/agent-net/internal/protocol"
)

// itemKind is the kind in an item identity, which is the JSON array
// [kind, name, hash]. An identity that cannot be read is a skill, the kind
// every item had before prompts and agents existed.
func itemKind(item string) string {
	var identity []string
	if json.Unmarshal([]byte(item), &identity) != nil || len(identity) != 3 || identity[0] == "" {
		return protocol.InventorySkill
	}
	return identity[0]
}

// fileKindRoot says whether root is a folder that holds items of this kind.
func fileKindRoot(kind, root string) bool {
	for _, r := range protocol.FileKindRoots(kind) {
		if r == root {
			return true
		}
	}
	return false
}

// installStaged moves a verified staged bundle to its destination without
// replacing anything. A skill is the staged folder itself; a prompt or an agent
// is the one file inside it, and the staged folder is removed afterwards.
func installStaged(kind string, stage, dest skillPath, entry string) error {
	if !protocol.IsFileKind(kind) {
		return renameSkillAt(stage, dest, true)
	}
	if err := renameSkillAt(stage.join(entry), dest, true); err != nil {
		return err
	}
	return stage.removeAll()
}
