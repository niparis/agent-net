package daemon

import (
	"encoding/json"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
	"github.com/niparis/agent-net/internal/settingsview"
)

// machineWorkerOverrides are the explicit machine choices loaded from node.json.
func (d *Daemon) machineWorkerOverrides() map[string]json.RawMessage {
	s := d.cfg.Workers
	out := map[string]json.RawMessage{}
	put := func(key string, v any) { out[key], _ = json.Marshal(v) }
	if s.Max > 0 {
		put("workers.max_per_machine", s.Max)
	}
	if s.CPUTarget > 0 {
		put("workers.cpu_target", s.CPUTarget)
	}
	if s.MemReserve > 0 {
		put("workers.memory_reserve", s.MemReserve)
	}
	for _, f := range []struct {
		key   string
		value time.Duration
	}{
		{"workers.idle_timeout", s.Idle}, {"workers.max_age", s.MaxAge}, {"workers.caller_gone_grace", s.OwnerGrace}, {"workers.queue_timeout", s.QueueTimeout},
		{"workers.cache_max_age", s.CacheMaxAge}, {"workers.tmp_max_age", s.TmpMaxAge},
	} {
		if f.value != 0 {
			val := f.value
			if val < 0 {
				val = 0
			}
			put(f.key, val.String())
		}
	}
	if s.StartGap != 0 {
		gap := s.StartGap
		if gap < 0 {
			gap = 0
		}
		put("workers.start_gap", gap.String())
	}
	return out
}

func (d *Daemon) workerSettingsWith(snap settings.Snapshot, repo string) WorkerSettings {
	// Auxiliary, non-recipe timing fields still use their existing defaults.
	local := d.cfg.Workers
	result := local.filled()
	where := settings.Where{Repo: repo}
	decode := func(key string, v any) {
		if e, err := snap.Resolve(key, where); err == nil {
			_ = json.Unmarshal(e.Value, v)
		}
	}
	var max int
	decode("workers.max_per_machine", &max)
	if local.Max <= 0 && max > 0 {
		result.Max = max
	}
	var target float64
	decode("workers.cpu_target", &target)
	if local.CPUTarget <= 0 && target > 0 {
		result.CPUTarget = target
	}
	// A computed reserve depends on current RAM; leave it unset so admission
	// computes it from its single load sample. An explicit network value wins.
	if local.MemReserve == 0 {
		if e, err := snap.Resolve("workers.memory_reserve", where); err == nil && e.Source != "network default" {
			_ = json.Unmarshal(e.Value, &result.MemReserve)
		}
	}
	for _, f := range []struct {
		key   string
		local time.Duration
		out   *time.Duration
	}{
		{"workers.idle_timeout", local.Idle, &result.Idle}, {"workers.max_age", local.MaxAge, &result.MaxAge},
		{"workers.caller_gone_grace", local.OwnerGrace, &result.OwnerGrace}, {"workers.queue_timeout", local.QueueTimeout, &result.QueueTimeout},
		{"workers.start_gap", local.StartGap, &result.StartGap},
		{"workers.cache_max_age", local.CacheMaxAge, &result.CacheMaxAge},
		{"workers.tmp_max_age", local.TmpMaxAge, &result.TmpMaxAge},
	} {
		if f.local == 0 {
			var value string
			decode(f.key, &value)
			if n, err := time.ParseDuration(value); err == nil {
				if n == 0 && (f.key == "workers.start_gap" || f.key == "workers.cache_max_age" || f.key == "workers.tmp_max_age") {
					n = -1
				}
				*f.out = n
			}
		}
	}
	return result
}
func (d *Daemon) workerSettings(repo string) WorkerSettings {
	return d.workerSettingsWith(d.SettingsSnapshot(), repo)
}

func (d *Daemon) workerSettingsViews(snap settings.Snapshot, history []protocol.SettingsHistory, q protocol.SettingsReadRequest, owner bool) ([]protocol.SettingView, error) {
	return d.workerSettingsViewsAt(snap, history, q, owner, nil)
}

func (d *Daemon) workerSettingsViewsAt(snap settings.Snapshot, history []protocol.SettingsHistory, q protocol.SettingsReadRequest, owner bool, target *protocol.SettingsRef) ([]protocol.SettingView, error) {
	views, err := settingsview.ViewsWithTarget(snap, history, q, owner, settings.Where{Host: settings.Host{RAMBytes: int64(d.load().Total)}}, target)
	if err != nil {
		return nil, err
	}
	overrides := d.machineWorkerOverrides()
	for i := range views {
		if value, ok := overrides[views[i].Key]; ok {
			views[i].Value = value
			views[i].Source = "node.json on " + d.hostName()
			views[i].Actor = ""
		}
	}
	return views, nil
}
