package gateway

import (
	"reflect"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestQualityPreviewDoesNotChangeRotation(t *testing.T) {
	rotation := protocol.WorkerRotation{Levels: map[string]*protocol.WorkerRotationLevel{"normal": {Current: map[string]float64{"x": 2}}}}
	before := rotation.Clone()
	candidates := []protocol.WorkerModelCandidate{{Harness: "codex", Model: "gpt-6.1-terra"}}
	picks := previewQualityChoices(candidates, nil, nil, nil, nil, rotation, protocol.ProtectedProviders{})
	if len(picks) == 0 {
		t.Fatal("preview chose no model")
	}
	if !reflect.DeepEqual(before, rotation) {
		t.Fatal("preview changed rotation")
	}
}
