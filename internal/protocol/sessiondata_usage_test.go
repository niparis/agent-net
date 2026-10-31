package protocol

import (
	"encoding/json"
	"testing"
)

func TestMachineUsageRoundTripsDiskFree(t *testing.T) {
	want := UsageReport{Machine: MachineUsage{Cores: 16, DiskFree: 31 << 30}}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got UsageReport
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Machine.DiskFree != want.Machine.DiskFree {
		t.Fatalf("disk_free = %d, want %d (%s)", got.Machine.DiskFree, want.Machine.DiskFree, raw)
	}
}
