# 0066 - Herdr only for session panes

**Date:** 2026-11-05
**Status:** Accepted. Amends decision 0046.

## Decision

Use herdr for Pi start and resume, including requests from outside herdr. A
missing herdr is an error with the searched folders. Stop closes the herdr
pane holding Pi. Enter no longer searches tmux. Maintaining two pane systems
adds unnecessary work.

A Pi session already running in tmux keeps running after upgrade. Enter shows
a note to open its existing window and stop asks the owner to end it there.
Once it ends, resume opens the saved conversation in herdr.

The other herdr entry rules in AGENTS.md and decision 0046 still apply.
