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

Migration 121 stores the requested Agent, start reference, working directory and
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

A crash or failed response before binding leaves a durable hold, not a fallback
to another Agent. It has no automatic expiry: an owner must bind the exact
native ID through `session.agent.reserve` using the recorded start reference.
Recovery must never choose the first session found in a directory. This can
briefly hold concurrent new sessions of the same harness; sessions that existed
before the start continue normally.
