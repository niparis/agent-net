# 0050 - Machine removal and token rotation

Date: 2026-11-03

The owner needs to stop trusting a lost machine without losing its history.
Owner operator actions `machine_remove` and `machine_rotate` use the existing
process gate. The gateway also checks an online owner session of the sending
node and an owner actor. CTO calls use Settings proposals and require approval.

Removal replaces the unique, non-null token hash with a public tombstone,
clears any staged hash and marks the node removed. Sessions go offline in the
same transaction. Database guards prevent a concurrent sync from reviving them.
Authentication and live-link registration share the removal lock. The daemon
sends its node ID so a removed node receives a specific setup instruction even
though the gateway no longer has its token hash. The ID grants no authority.

Rotation stages a new token hash before sending the token on the existing TLS
link. The daemon writes a temporary 0600 node.json, syncs it, renames it and
syncs the directory. It preserves other settings and confirms the save. The
gateway promotes the staged hash and closes links using the old credential.
Reconnecting with the staged token also promotes it: this recovers a saved token
when the acknowledgement was lost. Until confirmation the old token works.
Offline machines cannot rotate. A failed save does not revoke the active token.

Settings uses x for removal and r for rotation, with named confirmation prompts.
