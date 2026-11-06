# Agent selection before session registration

Date: 2026-11-06
Status: partial implementation

A session start ID currently identifies a daemon polling record. Discovery does
not report it, so the gateway cannot match a registering session to that ID.
Workers have their own start reference, which owner sessions do not carry.

Reserve the chosen Agent against the authenticated machine, harness and native
session ID. Validate the owner and live Agent before launch. Check the Agent
again and consume the reservation in the same transaction as registration.
Migration 121 stores these reservations across gateway restarts.

Pi creates its UUID before opening herdr. Claude reports its native ID after
background launch. For explicit Claude selection, hold registration during that
launch and reservation, and send the first prompt through the gateway only
after registration. Ordinary starts and old daemons keep their current default.

OpenCode and Codex explicit selection are refused before launch in this partial
implementation. OpenCode creates its ID through the service API; a plugin hello
may register synchronously during that call. Holding registration across the
API call can deadlock. Codex owner starts return a CLI launch plan and do not
provide a thread ID. Neither may be matched by folder because another session
can start there concurrently.

Remaining work: introduce an authenticated start reference carried by those
harnesses, or create their native sessions before opening their owner views.
Negotiate target capability before allowing explicit Agent selection in remote
starts; these currently fail before relay to prevent an old daemon silently
ignoring the selection. Also persist a pending Claude start before launch so a daemon crash between
launch and reservation cannot lose the selection. This implementation is not
the completed all-harness start protocol.
