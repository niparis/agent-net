package gateway

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

func stepAboutPrefix(changeID string, revision int, ref protocol.StepRef) string {
	return fmt.Sprintf("%s:%d:%s:%s:%d:", changeID, revision, ref.Step, ref.For, boolInt(ref.AfterMerge))
}

func stepAboutID(changeID string, revision int, ref protocol.StepRef, commit string) string {
	return stepAboutPrefix(changeID, revision, ref) + commit
}

func parseStepAboutID(aboutID string) (changeID string, revision int, ref protocol.StepRef, commit string, ok bool) {
	parts := strings.SplitN(aboutID, ":", 6)
	if len(parts) != 6 || parts[0] == "" {
		return "", 0, ref, "", false
	}
	rev, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, ref, "", false
	}
	return parts[0], rev, protocol.StepRef{Step: parts[2], For: parts[3], AfterMerge: parts[4] != "" && parts[4] != "0"}, parts[5], true
}
