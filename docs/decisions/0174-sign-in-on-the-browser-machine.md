# 0174 - Sign in on the browser machine

**Date:** 2026-11-18
**Status:** Accepted.

The Alibaba CLI listens on loopback, so a login on another machine cannot
receive the browser callback. Safari also blocks the callback in the owner's
setup. The owner asked for the listener to start where the browser runs, and
for the Desk to say to use Firefox.

The owner TUI runs the fixed console login locally. For a remote ask, the CLI
writes a fresh token to an isolated temporary configuration. The daemon imports
it locally and relays it through the gateway to the target daemon, which feeds
it to the fixed CLI import command on stdin. The gateway resolves the target
from the open ask and does not persist the token. The machines recheck quota
using the CLI configuration that quota-axi reads. A web click delegates to an
owner TUI. This replaces the remote pane path in decision 0132 for new clicks.
