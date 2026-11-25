package daemon

// The Claude worker window migration. An older agent-net registered the plugin
// from a local marketplace; the migration refreshes that copy while it runs and
// moves or repairs it. It runs in a goroutine, never on the start path, and it
// runs again on the integrations tick so a settings loss that happens while the
// daemon is up is repaired without a restart.

// migrateClaudeWorkers runs one migration at a time. A second caller returns;
// the next integrations tick tries again.
func (d *Daemon) migrateClaudeWorkers() {
	if d.cfg.MigrateClaudeWorkers == nil {
		return
	}
	if !d.claudeMigrateMu.TryLock() {
		return
	}
	defer d.claudeMigrateMu.Unlock()
	if note := d.cfg.MigrateClaudeWorkers(); note != "" {
		d.Logf("agent files: claude workers mod: %s", note)
	}
}
