# Agent selection before session registration

Date: 2026-11-06
Status: implemented

A session start ID originally identified only a daemon polling record. Neither
that ID nor the CTO proposal ID reached discovery registration. Workers have
their own start reference; owner sessions did not.

An explicit Agent selection is validated through the authenticated machine's
owner pseudo-session before launch. The daemon admits it through owner starts
or approved CTO proposals, using the existing authority boundary. The gateway
rejects unknown or tombstoned Agents and checks again when binding.

Migration 123 stores the requested Agent, start reference, working directory and
the set of existing session IDs before launch. Until the harness returns its
native ID, new lasting sessions of that harness on that machine have no final
Agent. The gateway holds their delivery and refuses context resolution. Existing
sessions and workers remain unaffected. Only one such unresolved launch per
machine and harness is allowed.

The directory is not used to guess an identity. The exact returned native ID
binds the session, in a transaction that either reserves its first registration
or assigns an already registered, held session. Binding releases the hold;
unrelated new sessions receive their normal Repo-owner defaults. Registration
and assignment cannot expose a different Agent to the first delivered message.

Pi supplies its UUID before launch, so its first registration already selects
the Agent. Claude returns a native ID after background launch. OpenCode returns
one from its creation API; its plugin may register synchronously during that
call, so registration must be allowed while delivery is held. Their selected
first prompts wait for binding and registration.

For selected Codex starts, a temporary connection to the existing shared app
server creates a fresh thread and returns its ID. After binding, the owner view
runs `codex resume <id>` with the requested first prompt. Creating a thread does
not start a model turn. The temporary connection closes; the long-lived peer
adapter remains read-only and never resumes an existing thread. This narrowly
extends decision 0012 for owner-requested creation. The protocol is documented
in [the official app-server documentation](https://learn.chatgpt.com/docs/app-server).

Remote starts first require the gateway's Agent-selection API, then the
destination's `session_agents` hello capability.
Old destinations cannot silently ignore a requested Agent. Requests without
Agent selection and old daemons retain their existing Repo-owner defaults.

A crash or failed response before registration leaves a durable hold for at most
two minutes from preparation, enforced by `sessionAgentStartTimeout`. This is a
constant, not a setting. A daemon failure report cancels the reservation sooner.
Both unbound holds and bound IDs awaiting first registration share the original
deadline. Expiry or cancellation marks the start failed, releases unrelated
sessions to their normal Repo-owner defaults, and permits another selected start.
The gateway sweeps every second and before registration, binding, status reads
and delivery checks. The daemon checks the durable failure status before sending
a selected first prompt or returning a successful polled start.

A late native ID cannot revive a canceled choice. A session registering later
uses its Repo-owner default. One owner Inbox item says "Agent choice for <start>
failed; <session> uses the default <agent>". If the native ID was lost, the item
names the new harness session without guessing which concurrent session it was.
When an exact ID arrives later, the gateway updates that same item with the
session and its actual default. Repeated reports and expiry create no duplicates.
Existing sessions and workers remain unaffected by these bounded holds.
