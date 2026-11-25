package daemon

import "context"

// The Claude worker window migration. An older agent-net registered the plugin
// from a local marketplace; while that copy is the one running, the daemon
// refreshes it. The daemon never moves or removes it: setup --claude-workers-mod
// and the off switch do that. It runs in a goroutine, never on the start path,
// and it runs again on the integrations tick so a panel change reaches a
// running copy without a restart. ctx is the daemon's, so stop cancels a
// running claude command.

// migrateClaudeWorkers runs one migration at a time. A second caller returns;
// the next integrations tick tries again.
func (d *Daemon) migrateClaudeWorkers(ctx context.Context) {
	if d.cfg.MigrateClaudeWorkers == nil {
		return
	}
	if !d.claudeMigrateMu.TryLock() {
		return
	}
	defer d.claudeMigrateMu.Unlock()
	if note := d.cfg.MigrateClaudeWorkers(ctx); note != "" {
		d.Logf("agent files: claude workers mod: %s", note)
	}
}
