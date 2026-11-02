# Development machine scripts

These scripts are for machines where agent-net itself is developed. Users of
agent-net never need them.

## gocache-trim.sh

Workers that build agent-net share one Go build cache, `/tmp/agent-net-gocache`,
and one build temp folder, `/tmp/agent-net-gotmp` (see `.agent-net/worker.md`).
Go never trims its cache by size, so the script:

- deletes cache files unused for two hours;
- above 15 GiB, deletes the least recently used files until the cache is under 10 GiB;
- deletes build temp folders with nothing written in the last two hours.

Run it every hour with a systemd user timer:

```
install -m 755 scripts/dev/gocache-trim.sh ~/.local/bin/agent-net-gocache-trim
mkdir -p ~/.config/systemd/user
printf '[Unit]\nDescription=Trim the agent-net dev Go cache\n\n[Service]\nType=oneshot\nExecStart=%%h/.local/bin/agent-net-gocache-trim\n' > ~/.config/systemd/user/agent-net-gocache-trim.service
printf '[Unit]\nDescription=Trim the agent-net dev Go cache every hour\n\n[Timer]\nOnBootSec=10min\nOnUnitActiveSec=1h\n\n[Install]\nWantedBy=timers.target\n' > ~/.config/systemd/user/agent-net-gocache-trim.timer
systemctl --user daemon-reload
systemctl --user enable --now agent-net-gocache-trim.timer
```

To stop it: `systemctl --user disable --now agent-net-gocache-trim.timer`.
