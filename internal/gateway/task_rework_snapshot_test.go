package gateway

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The optional owner-provided snapshot remains read-only. Opening the copy
// runs the real migrations and one-time backfills before reading panel data.
func TestTaskReworkSnapshot(t *testing.T) {
	const dir = "/tmp/agent-net-gwcopy"
	raw, err := os.ReadFile(filepath.Join(dir, "gateway-2026-11-01.db"))
	if os.IsNotExist(err) {
		t.Skip("gateway snapshot is not present")
	}
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "gateway.db")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	type row struct {
		level, family string
		tasks, mid    int
		headache      float64
	}
	var got []row
	for _, level := range s.QualityAnalysis().Levels {
		for _, f := range level.Families {
			if f.Tasks >= 5 {
				got = append(got, row{level.Difficulty, f.Family, f.Tasks, f.Midrun, f.Headache})
			}
		}
	}
	sort.Slice(got, func(i, j int) bool {
		if got[i].level != got[j].level {
			return got[i].level < got[j].level
		}
		if got[i].tasks != got[j].tasks {
			return got[i].tasks > got[j].tasks
		}
		return got[i].family < got[j].family
	})
	expected, err := os.ReadFile(filepath.Join(dir, "expected.txt"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`^(\S+)\s+(\S+)\s+tasks\s+(\d+)\s+headache\s+([\d.]+) min\s+\(\+mid-run msgs\s+(\d+),`)
	want := map[string]row{}
	for _, line := range strings.Split(string(expected), "\n") {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[3])
		h, _ := strconv.ParseFloat(m[4], 64)
		mid, _ := strconv.Atoi(m[5])
		want[m[1]+"/"+m[2]] = row{m[1], m[2], n, mid, h}
	}
	if len(want) == 0 {
		t.Fatal("expected snapshot table is empty")
	}
	for _, r := range got {
		t.Logf("%-7s %-9s tasks %3d  headache %4.1f min  midrun %d", r.level, r.family, r.tasks, r.headache, r.mid)
		key := r.level + "/" + r.family
		w, ok := want[key]
		if !ok {
			t.Errorf("unexpected row %s", key)
			continue
		}
		if r.tasks != w.tasks || r.mid != w.mid || fmt.Sprintf("%.1f", r.headache) != fmt.Sprintf("%.1f", w.headache) {
			t.Errorf("%s got %s; want %s", key, fmt.Sprintf("%d %.1f %d", r.tasks, r.headache, r.mid), fmt.Sprintf("%d %.1f %d", w.tasks, w.headache, w.mid))
		}
		delete(want, key)
	}
	for key := range want {
		t.Errorf("missing row %s", key)
	}
}
