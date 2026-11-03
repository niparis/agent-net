package gateway

import (
	"strconv"
	"strings"
)

// Enrolled daemons supply actors after their owner gate has checked approval.
func validChangeActor(actor string) bool {
	if actor == "owner" || actor == "cto" {
		return true
	}
	const prefix = "owner (CTO proposal p_"
	if !strings.HasPrefix(actor, prefix) || !strings.HasSuffix(actor, ")") {
		return false
	}
	n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(actor, prefix), ")"), 10, 64)
	return err == nil && n > 0
}

// Only gateway-internal skill plans can use the standing choice actor.
// Incoming requests still require validChangeActor.
func validToolChangeActor(actor string) bool {
	return actor == "standing choice" || validChangeActor(actor)
}
