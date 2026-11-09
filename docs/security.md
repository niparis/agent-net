# Security

## Settings layer

Public settings, effective values, sources and history are readable by agents,
workers, bots and the CTO. Only personal owner preferences may be marked
owner-only. Do not put secrets in settings. Ordinary sessions cannot write
settings. Owner writes and undo pass the daemon owner gate. The private CTO
may write and undo settings at once. Every change is recorded, and per-address
expectations prevent an old write from silently replacing a newer value. Worker
hand-back, delegation and approval wording may be set at network or repo scope;
text has no automatic strictness order.
Required hand-back tool names and completion tokens are supplied by code, not
by the editable wording.

The gateway trusts the authenticated daemon's actor label, as for graph writes.
An enrolled machine token can therefore assert owner authority. A compromised
enrolled machine can change rules; revoke its token if control is lost. The
gateway requires a known settings actor label for `settings.write`,
`settings.validate` and `settings.undo`, and checks its class, address, value
and bound in its transaction. Those frames reject missing and unknown labels.
The older `node.settings.set` frame may omit the label for compatibility with
older daemons and is then treated as owner, as before. If it supplies a label
for a moved setting, the label must be valid. Current daemons send it explicitly.
An old settings proposal is validated against the current state before it runs.
Text settings cannot contain managed-block or agent-net
notice markers, apart from the standard guidance's worker-brief reference.
Worker delivery text cannot contain a task boundary. The completion checklist
marker is fixed framing, separate from editable advice.

## Gateway exposure

Go profiling is disabled unless the process has `AGENT_NET_PPROF` set.
The gateway and daemon accept only a literal loopback IP for that listener.
Profiles use a separate HTTP mux and are not added to the gateway routes.
The profiling listener has no authentication; local users can read profiles
while it is enabled.


The gateway listens on `0.0.0.0:7420` by default, so it accepts connections on every network interface. These routes do not require a node token: `GET /v1/ca` serves the public CA certificate, `GET /install/bin/{os}/{arch}` serves the install binary, `GET /install/version` returns the release version, and `POST /v1/enroll` accepts a one-use enrollment code. `/install/checksums.txt` and `/install/checksums.txt.sig` are also public, and `/install/{code}` is gated by the one-use install code rather than a node token. These routes support joining and updating machines. The enrollment code expires after 10 minutes and can be used once. The join flow also checks the CA fingerprint. The other operational API routes require an enrolled node token or the local admin token.

This fits a LAN or tailnet where the gateway port is reachable only by machines you control. Do not expose port 7420 to the open internet. The public routes would be reachable by anyone, and the enrollment route could be probed or abused if an invite code leaked. Keep invite codes private.

The daemon runs each installed harness's own update command as the current user, with input closed and a five-minute timeout. It does not run sudo. The commands can change the harness installation and may access the network. Busy harnesses are skipped and retried later. The owner can turn this off for the network with **Keep harnesses updated** in Settings under Integrations. Update results, including the last line of a failed command's output, are sent with the machine's harness inventory.

Decision [0036](decisions/0036-primitives.md) distinguishes authority over actions from autonomy over decisions about work. An agent is a chartered identity; a session is its running process. The checks below describe current behavior, including the autonomy setting and its guard rails.

The network-wide CTO harness, model preference and auto-compaction rules are
owner settings. The daemon's `cto_settings` method uses the owner gate and
refuses agent sessions. Values are bounded: a threshold from 10 to 100 percent,
a floor up to 10 million tokens, and cache windows up to one day. The
owner-facing CTO title is also an owner setting, stored at the gateway as
`cto_title`: 1 to 24 printable characters, trimmed, and refused when it
contains `[agent-net` or `<cross-session`, so it cannot imitate a note that
agent-net writes. Agents never receive it: the notes and guidance they match
always say `CTO`. Like the
integration settings, the gateway endpoint requires an enrolled node token;
that token is an owner credential and must not be shared with agents. The owner sends to the CTO with `owner_send` and reads its timeline with
`session_text`; `session_enter` enters it. `cto_new`, `cto_settings` and
`cto_graph_history` stay. All use the same gate. The daemon keeps CTO action lines in its private state database;
these can include message and task text.

Model score overrides, difficulty minimums, the worker provider allowlist and worker quota floors are owner settings. The daemon's
`scores_get`, `scores_set`, `difficulty_get`, `difficulty_set`, `worker_providers_get`, `worker_providers_set`, `protected_providers_get`, `protected_providers_set`, `worker_floors_get` and `worker_floors_set` methods use the
owner gate and refuse agent sessions. Their gateway endpoints require an
enrolled node token, which is an owner credential. Scores and minimums are
limited to 0 through 100. Worker floors are limited to 0 through 95. The CTO's `settings_change` with `area: score` changes
the same settings at once.

The daemon sends each worker brief to the configured difficulty judge before it asks the gateway to choose a model. The default is OpenCode with the DeepSeek judge model. The judge's label is the level the picker uses. A missing judge, a failure, a timeout or low confidence falls back to normal and never blocks the start. The gateway stores the scores and judge reasons, which may reflect the brief, but not the brief itself. Enrolled nodes and agents can read assessments with worker records. The assessment write is a daemon-to-gateway operation: the gateway accepts it only from the node that owns the worker session.

Worker profiles are stored at the gateway. The list is public: any session except a worker, the CTO included, may read it. The owner CLI and bound non-worker agent sessions may add or remove them. The private CTO may add or remove profiles at once. The daemon and gateway both reject worker sessions, and the gateway verifies that the requesting session belongs to the sending node. It resolves each added model against the advertised model list of a machine and records the requester and time. The gateway does not verify that an agent received an owner's instruction, so agents must follow their normal instruction and approval rules. Registered profiles remain subject to the owner-controlled worker provider filter. Automatic choice also requires the task to be at the profile's maximum difficulty or exactly one level below it. The maximum is the highest level whose score minimum it meets. Ineligible profiles have no rotation weight. When none qualify, the start fails with registration guidance; explicit model requests override this eligibility rule and the start note says when they do. Recent failure and success marks apply only to the same node and harness.

The OpenCode CTO server binds only to localhost and requires a random password.
Its data, state, configuration and cache are private to the daemon. The OpenCode
agent-net plugin is disabled in that process. The private configuration copies
the owner's OpenCode provider and other general settings, without plugins.
It excludes the owner's agents, MCP servers, permissions and instructions to keep
the CTO's tools private. The Claude Code CTO uses the
owner's Claude login and a background session in a private working directory.
Its session receives `--strict-mcp-config` and a private MCP configuration that
contains only `agent-net cto-mcp`, not the ordinary agent-net MCP server. The
daemon excludes this working directory from ordinary Claude discovery so it
cannot get ordinary agent tools or a second network identity. The owner-gated
CTO start registers a separate stable session at the gateway. New CTO sends
use that session. Incoming messages go to the active harness and are labeled
as peer input with the body quoted as data. For 30 days after the gateway upgrade, the gateway routes replies to older owner-session CTO
conversations to the CTO session. It checks the stored root marker and creation
time; personal owner conversations remain with the owner.

The CTO has no built-in shell, file, search, web, scheduling or subagent tools.
Claude uses `--tools ""`, empty settings sources, disabled hooks, no enabled
plugins and strict MCP configuration. OpenCode denies every built-in action
with a wildcard deny plus explicit deny rules for bash, edit, write, subagent,
read, glob, grep, list, webfetch and todowrite. Only the private CTO MCP namespace
is allowed. The general subagent is disabled and owner plugins are not copied.

Startup checks capabilities instead of a version allowlist. Harness versions
are logged for diagnosis and do not decide whether the CTO can start.
Claude's `--help` must list `--tools` and `--strict-mcp-config`; a missing
flag stops startup. Its tool set is enforced by the start arguments and private
settings. There is no independent effective-tool enumeration on this path.

An isolated probe of Claude Code 2.1.316 used a temporary HOME, a fake API key,
an unreachable API URL and no prompt. With the CTO's start arguments it printed
a plain-text `backgrounded` notice and instructions to attach, read logs and
stop the session. Standard error said `Starting background service`.
There was no JSON system init record or effective-tool list. The probe's job
was stopped afterward. Startup therefore does not depend on such a record.

OpenCode's `GET /api/agent` must report every required deny rule in the CTO's
effective permissions, including the wildcard deny that closes its default
allow. No later rule may allow tools outside the private CTO MCP namespace,
and the general subagent must be absent or disabled. Missing reports or deny
rules stop startup. Fake harnesses test these checks. Real model tool execution
and subagent inheritance have not been verified.

The private MCP server classifies calls as public read, talk, immediate change
or proposal. Only node removal, agent tombstone, repo archive, machine removal
and machine token rotation are proposals. The CTO runs other changes at once;
the owner sees Inbox notices and can undo supported changes. A proposal still
requires a dialog or Inbox approval.

The CTO can stop, resume and restart an owner or agent session with
`session_change` (actions `stop`, `resume`, `restart`). These are immediate
changes with an Inbox notice and an audit line. They add no new way to signal a
process: `stop` and the stop inside `restart` use the owner's guarded stop, which
signals only the registered harness process and rechecks its identity before it
escalates. For a session on another machine they use the owner session frames,
which the receiving machine checks against the gateway. They refuse workers
(which have `worker_change`), the CTO's own session, and Pi for resume and
restart. A resume never opens a view and carries no herdr location. A CTO can
therefore interrupt a running turn of any agent session, as the owner can; the
conversation is kept, and the Inbox notice for a stop can resume it.

**Connected apps.** The private CTO may list connected apps and public-access
status, grant or remove a bot's shared MCP server access, rename an app, revoke
it, and switch internet access on or off through `mcp_change`. These calls use
the existing owner handlers after the CTO process-tree check and explicit
classification. Ordinary agent and worker access stays refused. The app list
includes names, client metadata, grants and revocation state, never credentials.
This is an explicit exception to the public-read rule below, authorized by the
owner's request for CTO operator access on 2026-11-08.

A grant is still only `mcp server <name>`, only for an active bot and a registered
server. The server's allowed-tool filter and per-app call limit still apply.
Removing a grant takes effect on the next request. Rename still requires a
unique active name and updates the bot session. Revocation invalidates app
credentials and may switch access off when no active apps remain. It cannot
restore credentials through undo; reconnecting needs owner consent. Public
access uses the gateway's existing listener and Tailscale or tunnel setup.
A new Tailscale login or OAuth consent still requires the owner.

Every CTO app call has a durable local audit line, including reads and failures.
Successful mutations also create a CTO notice in the Inbox and Desk log. Names,
grants and the public switch save their previous values for Inbox undo; a
request for an already-set value preserves that value on undo. Revocation has
no undo action. No extra approval gate is added. The CTO tool-list
regression test allows 36 KiB of serialized schemas, increased from 32 KiB to
keep connected-app and trust/standing-orders descriptions readable. This is a
test budget, not an authority check or a runtime request limit.

**Private owner operations.** On 2026-11-08 the owner authorized the CTO to
perform owner actions. `settings_change` area `operator` adds an explicit enum
of owner methods. The private process-tree check and action classification run
before the existing owner handlers, which retain their argument validation.
This route can read owner Inbox/Desk and asks, other sessions' text and stored
conversations, usage, inventory and skill files, full settings and playbooks,
worker selection data, graph history and delivery records. Ordinary agent
schemas and caller-scoped reads remain unchanged. The CTO must keep these
results private unless the owner asks it to share them.

New mutation powers include session pinning, stop/resume, archive/restore and
agent binding; hidden-session, quality, timezone, onboarding, worker-floor and
difficulty preferences; Team authority modes and held back text, delivery-owner
assignments and merger set/clear; tool copy/import/export, accepted versions and
MCP coverage plans/apply/undo; gateway backups, learned-suggestion answers,
delivery reports and defect reports; app refusal and public URL changes.
Node names, project registration/removal/display, Graph autonomy and worker
profiles already have private CTO paths. No new approval gate is added.

All calls record a durable CTO audit line, including failures and reads.
Changes also record Inbox/Desk notices. Supported undo stores the old value,
uses graph/settings history, or uses an existing inverse action. Merger undo
restores registrations, routing edges and pipeline settings. Composite undo
checks every operation before executing any, and cannot include app consent.
An archive preview has an audit line but no mutation notice or undo. Exports,
backups, imports, append-only reports and sent messages cannot be undone by
deleting their artifacts. Stop/resume inverses do not undo a session's work.

**Owner consent.** The CTO may open an app or phone pairing ask with
`mcp_change app_ask`, using a live pending code. The ask includes that code and
the requested app type/name. The owner answers through the existing Inbox
approval handler. The daemon supplies CTO attribution itself; a CTO answer
cannot approve this ask. Payload changes replace the ask ID, so an old approval
cannot authorize new permissions. Approval/refusal closes the ask in the same
transaction as the pending request; expiry and completion elsewhere also close
it. The CTO cannot invoke `apps.approve` through the operator or undo route.

Tailscale login and Funnel consent asks contain the HTTPS link. The TUI opens
it only after the owner's answer, with an HTTPS-only browser opener. Approval
in the browser remains the owner's action. The gateway retries the requested
public switch and closes the ask on success; switching off withdraws it.

**Owner UI and invites.** Session/CTO entry relays an identifier-only request
to one connected owner TUI, which retains its own herdr context and existing
entry rules. It cannot carry a command, start plan or terminal flags. A missing
owner TUI is an error. Optional `ui_node_id` selects the target. Private CTO
network identity recovery reuses the registered identity on its own node and
cannot move a CTO home registered elsewhere. Only an actual owner start moves
home. Gateway-hosted invite creation can be requested by the CTO from another
enrolled machine through the owner-validated gateway route.

Decision 0035 still governs ordinary agent-shaped CTO reads: they use the CTO
caller and ordinary checks. Decision 0097 adds the explicit private operator
exceptions above; it does not widen ordinary agents' reads.

Ordinary agent list/detail reads omit waiting-question contents. Owner CLI/TUI
reads retain them. Senders of requests to a waiting session and bots receive
only the waiting kind and start time in the waiting notice or send receipt,
never the question or tool summary. Agent
message lists force the bound caller's session ID;
message status requires that caller to be a sender or recipient and never
uses the owner-only body relay. The CTO uses those same checks on ordinary agent-shaped reads. Public model
playbook notes in `agent_machines_find` remain public; the owner-only full playbook
view is available through the private operator route. No ordinary agent read is widened.

**Team delivery.** Only the CTO can send to a group. A team selector uses typed membership and repo owner links to select online sessions. It grants no authority. Team recipients are resolved when the message is sent, so a member removed at that same moment can still receive that one message. Legacy team-send proposals check membership again when approved. An enrolled machine and the gateway remain trusted to report the graph and session identities.

**Project layout read.** The CTO read tool `projects_layout` scans disk under the home folder on online machines to report repository root locations, group folders, and origin remotes. The CTO can read folder names and remotes under the home folder on every machine. It is read-only: it does not register projects, execute scripts, or change files. It drops worktrees, caches, dependency folders, hidden folders, and temp folders, and is bounded by depth (at most 4 levels under home), result caps, and a 15-second timeout. Results are cached per machine for 10 minutes.

**Charters and rules are public.** The owner decided, twice, that every agent
may read the whole graph, read-only. `agent_graph_read` returns every node's charter
fields and maturity, every node's rules with name and value, and every edge's
kind, mode and request rule. The rules are the six node settings resolved
against the node's maturity (`tests_before_merge`, `merge_authority`,
`worker_sandbox`, `judge_consumers`, `deploy_rule`, `autonomy`) and the delegation policies
the owner set for the node's project on each machine (`delegate`,
`coder_model`, `main_model`, `workers`). `agent_session_get` puts the same rules on the
session's node, so a session can check its own rules directly. Delivery rules,
including merge authority and its displayed text, are settings-layer rules.
The owner and CTO can change them.
A repo with no graph node uses the network merge authority. The gateway supplies
the receiver's merge text with each delivered message, overriding any sender
value. The delegated approval modes are inserted into the rule text, so a
changed mode is not paired with the old list in that text. An older daemon or
extension ignores the new text field and uses its built-in wording until it is
upgraded. Only changes are owner-gated (`node_settings_set` and `session_policy`). What stays hidden
is not a rule: secrets and tokens, machine paths and locations, the
learned-change history with its evidence excerpts, and the owner's
hidden-items preferences. What stays hidden, and why:

| Hidden field | Reason |
|---|---|
| `locations` (machine and path) and `sessions` | A machine path and a session list are not a rule. They name your folders and the processes that happen to be open, so they stay owner-only. |
| Any setting name that is not one of the six above | An old or broken writer may have stored anything under an unknown name, including a path or a secret. The public read never reads it. |
| A secret or token in a known setting | `ValidateNodeSetting` allows only the listed values, so a secret fails validation and `NodeDefaults` falls back to the maturity default. A hand-written invalid value is never returned. |
| Learned-change history and evidence excerpts | The history can quote owner prompts, peer messages and reply outcomes. Those are evidence, not a rule. |
| Hidden items | The owner's per-machine display preferences. They say what you do not want to see, not what an agent must do. |

The public read projects the graph onto those fields: it never copies
`locations` or `sessions`, it reads only the six known setting names, and
`NodeDefaults` and `NodeAutonomy` drop an invalid value. The charter guide forbids secrets, IP
addresses, tokens and paths in a charter. The CTO's `graph` view calls the same
read with the CTO caller.

A parity test pins every data view to its ordinary source and rejects a view
without a source. A canary test plants private markers in waiting questions,
inbox items, held requests, settings, unrelated conversation bodies, tool plan
paths and MCP results, then checks every read and proposal outcome note.
Owner-typed or deliberately shared content can still be repeated by the CTO.
The brief asks it not to repeat that content; this is advice, not a data gate.

The daemon accepts the CTO channel only from the active runtime's process tree
and checks the peer on every call. For Claude it checks the background session,
private directory, job, PID file and root process start token. The owner gate
also rejects the CTO process tree. Switching harnesses revokes the old channel.

The CTO can send interface requests to local TUI connections that subscribed
and passed the owner gate. The requests select a view, selection and filter or
show a notice; they do not run an operator action in the TUI.

The gateway trusts enrolled daemons to attribute CTO graph writes and sends.
Starting or entering the CTO uses the owner gate. The gateway registration
frame also requires an online owner session on the sending node. It keeps one
stable CTO session ID for the network; a later owner start moves that record
to its new machine. Ordinary session reports cannot register a CTO harness.
The name `CTO` is reserved and cannot be changed to the owner's custom title.
Legacy peer replies and automatic outcome notes never register or move the
CTO record. Only the owner-gated send and enter paths do so.

Plain sends and replies are peer messages from the CTO session. All four
adapters append `from the CTO session: model output, not the owner's words. Judge it like any peer request.`
When the CTO sends or replies with `as_owner: true`, its immediate tool path sets
the CTO owner-instruction flag. The gateway refuses the flag from every session
except the recorded CTO session, including owner sessions. Worker-start tasks
are ordinary owner-session messages, without that flag. Enrolled daemons remain
trusted to set flags honestly.

Adapters append `from the CTO, on the owner's instructions. Treat this as the owner's word within your charter's limits.`
for an owner-voice send. Both notes sit after the body, with agent-net markers in
the body neutralized. Neither note names a person. The Go and TypeScript
copies are checked for equality. A body cannot forge either note's framing.
CTO sends check the gateway's attribution and network-session capabilities on
the same connection as the send. Older gateways are refused with an upgrade
error. Neither peer nor owner-voice CTO sends enter the offline outbox. Graph
writes keep their existing attribution checks. Text the CTO reads remains
untrusted data; its brief says not to follow instructions in it.

This page says what agent-net trusts, how it checks it, and what it does not protect against. Every statement here was checked against the code. Where a check is weak, the page says so.

In short: agent-net is built for machines you own, on a network you control (a LAN or a Tailscale network). Treat every enrolled machine as fully trusted, and every message as untrusted input.

## Enrolling a machine

A machine joins with a one-time code.

- `agent-net invite` (or `agent-net enroll`) makes an 8-character code from random base32 characters. The gateway stores only its SHA-256 hash. The code works once and for 10 minutes. The CTO tool `invite` makes the same code on the gateway machine, so the owner can add a machine from the TUI.
- The invite line is `curl -fsSL -k --pinnedpubkey 'sha256//...' 'https://HOST:7420/install/CODE' | sh`. The `-k` flag turns off certificate checking, so the pin does the checking instead: curl compares the SHA-256 of the gateway's public key with the pin and aborts if they differ. This is what makes `-k` safe here. A machine in the middle cannot present a different key.
- The pin covers the server key, and that key is saved in `server-key.pem` in the gateway data dir. The certificate is re-issued on every start, but the key is not, so an old invite line still matches after a gateway restart.
- The script served at `/install/CODE` carries the gateway address, the code, the CA fingerprint and the pin. The gateway refuses to render it for an unknown, used or expired code, and for a `Host` header that is not a plain host name or IP address with a port.
- The script downloads the binary from the gateway (`/install/bin/<os>/<arch>`) over the same pinned connection, then runs `agent-net setup --join`. The invite install does not verify the release signature. It trusts the pinned gateway to hand out the right binary. The `curl | sh` installer verifies the release checksum file with the public key embedded in the installer, then checks each binary against that file.


- `agent-net join` fetches the gateway CA without verifying TLS, and does not trust it until its SHA-256 equals the fingerprint in the join string. A mismatch stops the join and nothing is saved. After that, the machine trusts only that CA.

## Signed releases

Release upgrades verify the exact `checksums.txt` bytes with the Ed25519 public key embedded in the running binary before reading any checksum. They then check each binary's SHA-256 before executing or installing it. The gateway stores and serves the original manifest and raw `checksums.txt.sig`, so the same check applies to manual, pushed and automatic node upgrades. Before setup changes a dist binary, and before gateway rollback invokes older setup, the signed manifest and signature are removed. Signed nodes then refuse upgrades until the gateway receives a complete signed release. Older clients can still use gateway-generated unsigned checksums. An absent or invalid signature is refused. The public installer uses `ssh-keygen -Y verify` with its embedded copy of the same public key and the SSHSIG `checksums.txt.sshsig`. It fails closed if verification is unavailable. The signed list also covers `install.sh`; Pages verifies the release signatures and installer checksum before publishing. The initial install line still trusts HTTPS and the published Pages script. The installer accepts v-prefixed release version tags with optional pre-release and build suffixes, pins one tag for all downloads, and checks the local binary reports that tag. In gateway mode it copies the raw signature only after the SSH signature verifies; signed Go clients verify the raw signature themselves. The gateway invite install above trusts the pinned gateway, not the release signature. The gateway chooses which signed release it serves. Manual and pushed downgrades need an explicit `--force`; versions that cannot be ordered also need it unless they are identical. Automatic upgrades remain forward-only. Nodes running older versions may not enforce this rule until upgraded. Older installed versions do not gain signature checks until they are upgraded. Locally built dist binaries are unsigned and cannot be auto-upgraded by signed clients. See [RELEASING.md](../RELEASING.md) for key custody and rotation.

## Transport

- The gateway makes its own CA on first start (ECDSA P-256, valid 10 years) and a server certificate signed by it (valid 1 year, re-issued at each start). The CA key `ca.key` and the server key are written with mode 0600.
- The server certificate lists `localhost`, `127.0.0.1`, `::1`, the host name, every interface address and any `--san` names. If a node reaches the gateway under another name, the TLS check fails until you add it.
- The daemon and `agent-net join` use a TLS client that trusts only the pinned CA (minimum TLS 1.2), not the system roots. All traffic between a daemon and the gateway, including the WebSocket, goes over that connection.

## Files between machines

Files do not travel in messages. A message carries only a manifest: name, size, SHA-256 and an ID per file. The bytes go over Taildrop on the same tailnet, or through the gateway in parts when Taildrop is not available. Agent sessions use `agent_file_send`; workers attach files to `agent_handback`. The TUI flow is a later step (decision [0054](decisions/0054-files-between-machines.md)).

What the sending daemon checks, before the message is stored:

- **Limits.** At most 100 MB per file and 10 files per message. The gateway checks the manifest again when it stores the message: a file name is a plain name of at most 200 bytes (no `/`, `\`, control character, `.` or `..`), the IDs and names are unique, and each hash is a SHA-256.
- **Allowed folders.** A session can send only files in its project or worktree, or in `/tmp`. A worker can send only files in its own worktree with `agent_handback`; a worker without one cannot attach files. Symlinks are resolved first. For `agent_file_send`, a link must stay inside the most specific allowed project, worktree or `/tmp` folder that contains the named path. For a hand-back, the named path and the resolved file must stay inside the worker's own worktree. Confined sends refuse files with multiple hard links, because another link could lie outside the allowed folder. A path must be absolute and name a regular file. The file system root never counts as a project.
- **Never sent, whatever the project.** The credential folders and files under the home folder: `~/.ssh`, `~/.gnupg`, `~/.aws`, `~/.azure`, `~/.kube`, `~/.docker`, `~/.pki`, `~/.password-store`, `~/.netrc`, `~/.git-credentials`, `~/.npmrc`, `~/.pypirc`, `~/.config/gcloud`, `~/.config/gh`, `~/.config/hub`, `~/.local/share/keyrings`, `~/Library/Keychains`, and the harness logins `~/.claude/.credentials.json`, `~/.codex/auth.json`, `~/.local/share/opencode/auth.json`, `~/.pi/agent/auth.json`, `~/.gemini/oauth_creds.json`. Also agent-net's own folders: the daemon state folder, the config folder, the gateway data folder, `~/.local/state/agent-net`, `~/.local/share/agent-net` and `~/.config/agent-net`. One exception: a worker's worktree lives in the state folder, and it is that worker's project, so the worker may send files from its own worktree, and nothing else there.
- **Secret scan.** Every text file (no NUL byte and valid UTF-8 in its first 8000 bytes) goes through the file-send scanner, `redact.HoldsSecret`. A hit refuses the file. The refusal names the file but never repeats the text. Only the owner can send such a file anyway (a flag the owner flow sets; no agent tool sets it). Binary files are not scanned. The scanner has two kinds of rule:
  - **Known shapes always refuse.** A private key block, a `sk-` key, a GitHub token (`ghp_` and kin, `github_pat_`), an AWS key ID, a Slack `xox` token, a Google `AIza` key, a GitLab `glpat-` token, or a JWT.
  - **Name rules refuse only a value that looks random.** The names are `token`, `key`, `secret`, `password`, `auth` and `credential` followed by `=` or `:`. The rules also cover a bearer or `Authorization` value, a password in a URL, and a `--password=` style flag. A value is a secret when it has all of these: 16 or more characters; one token of letters, digits and `_ - + / = ! @ # & * ^ ~`; a letter and a digit; at least eight different characters. It is not a secret when it is a path (`/tmp/x`), an upper case name (`MY_APP_KEY_2`) or an identifier. An identifier is words joined by `_` or `-`, or camel case, with runs of letters of three or more (a version tag such as `v2` is fine), runs of digits of three or fewer, and at most two in five letters capitals. So `key='cto_session'`, `token := req.Token`, `auth: true`, `secret = os.Getenv("X")` and `password: ${DB_PASSWORD}` are sent.
  - **Not caught by a name rule:** a value of letters only, a value with a dot, a space or a `$`, and a placeholder such as `<token>`. A human password made only of words can pass.

  Logs and messages use `redact.Secrets`, which is unchanged. It masks every name rule whatever the value looks like.
- **A snapshot is sent.** The daemon opens each component of the resolved path without following symlinks, checks that the open descriptor names the same regular file and copies from that descriptor into `<state>/files/outgoing/` while it hashes it. A replaced path is refused. A file that changes after the snapshot cannot reach the receiver under the old hash. The copies are deleted when the send ends, and a crash leftover after a day.

How the bytes travel:

- **Unique names.** Every file travels as `agent-net-<message id>-<file id>-<name>`, over Taildrop and through the gateway. The receiving daemon takes a file only when its name is announced by a message it is collecting and its size and SHA-256 match the manifest. It never follows a symlink left under an announced name.
- **Taildrop** is used only when the receiving machine reported a Tailscale address that this machine's `tailscale status` lists as an online peer, so both machines are on one tailnet, and the receiver's daemon does not run on macOS. A receiver on macOS, or one whose OS is unknown, gets files through the gateway: a Mac daemon never reads `~/Downloads`, where an open can block in the kernel (decision [0125](decisions/0125-worker-file-notice-timeout.md)). A Taildrop that fails falls back to the gateway. Worker notices use the gateway between machines. A message held for the owner's approval keeps its files on the gateway, which deletes them after 7 days if nobody approves.
- **The gateway** stores the parts (below). The receiver finds an upload by its unique name with `xfer.find`, which lists only finished uploads addressed to the calling machine.
- **Same machine.** Between two sessions of one machine the copy moves through `<state>/files/local/`, never through the network.

A peer may send you up to 10 files of 100 MB each with a message. A peer controls the contents and names of those files; a matching hash verifies transfer integrity, not that the contents are safe. The secret scan is on the sending daemon and does not scan binary files.

What the receiving daemon does:

- **Inbox.** Each file goes to `~/.local/state/agent-net/daemon/inbox/<session>/<message>/<name>`, folders 0700, files 0600. Only the receiving session's folder gets the files. An ordinary message reaches the agent only when every file is in; its text then says where the files are. A worker notice is delivered after at most 2 minutes while the receiving daemon is online, even if files are missing, and identifies any missing names.
- **Hash mismatch.** A file that fails its size or hash is not taken. When the same copy fails twice in a row (so a file still being written is not refused), an ordinary message fails with the file's name. A worker notice still reaches its caller, without the bad file, and lists it as missing.
- **Files nobody announced are not taken.** On macOS the Tailscale app saves Taildrops straight into `~/Downloads`; the daemon picks up only announced names there and leaves everything else, including a bad copy. On Linux the daemon runs `tailscale file get` into `<state>/files/taildrop/` only while it collects files, and moves every file nobody announced (and a bad copy) to `~/Downloads` under a free name, never over an existing file, so the owner's own Taildrops still arrive.
- **Retention.** The daemon deletes a message's inbox folder 7 days after its last file arrived.
- **What the log holds.** The daemon logs the message ID, file IDs, sizes and paths. It also logs missing file names when it delivers a worker notice without them. It never logs file contents.

The gateway transfer in parts:

- **The gateway stores parts, not a trusted file.** Each part is 1 MB and carries its own SHA-256. The gateway checks the part hash, the length and, at the end, the whole SHA-256. It does not scan the bytes for secrets; the daemon does that before it sends.
- **Who may act.** Only an enrolled machine with a valid node token can use the frames at all. Only the sending machine may begin, upload or resume an upload. Only the machine the sender named as receiver may download or confirm it. Any other enrolled machine is refused, and so is the sender.
- **Limits.** One upload holds at most 100 MB. A sending machine may keep at most 1 GB staged in the gateway at once, which is one maximum message (10 files of 100 MB). An upload that would exceed either limit is refused before its first part is stored. A file name is at most 255 bytes and holds no control characters.
- **Retention.** The parts are deleted as soon as the receiver confirms the whole file. Every upload is also deleted 7 days after it began, downloaded or not. The hourly maintenance deletes expired parts even when no transfer frame arrives.
- **Where the bytes are.** Under `<gateway data>/transfers/<upload id>/`, folders 0700 and files 0600. They are never written elsewhere. A `gateway backup` does not copy parts that are still in flight; they expire within 7 days.
- **What the log holds.** The gateway logs the upload ID, the sender, the receiver, the size and the part count. It never logs the file name or the bytes.

## Node tokens

- At enrollment the gateway gives the machine a node ID and a token (32 random bytes, hex). It stores only the SHA-256 hash of the token.
- The token is saved in `~/.config/agent-net/node.json` with mode 0600. The daemon sends it as a bearer token when it opens its WebSocket. One node has one live connection, and a new one replaces the old.
- The gateway only accepts a message from a session that belongs to the node on the connection. A node can directly read messages sent from or to its own sessions. The owner can also request another message through the relay described below. It can list every session on the network and read the event feed (metadata, no bodies; see [the event feed](#what-every-node-can-read-the-event-feed)).
- In Settings, select a machine and press `x` to remove it, or `r` to rotate its token. Both ask for confirmation with the machine's name. Only the owner can act. CTO requests become proposals for owner approval.
- Removal deletes the token hash, marks the machine removed, closes its live link, and marks its sessions offline immediately. History rows stay. Reconnecting is refused; the daemon logs `this machine was removed from the network; run agent-net setup to join again`.
- Rotation requires an online machine. The gateway stages a new hash and sends the token over its pinned TLS link. The daemon atomically replaces `node.json` with mode 0600, preserves other settings, and confirms the save. Confirmation revokes the old token and closes any link still using it. The daemon reconnects with the new token. A reconnect with the staged token also confirms it, so losing the acknowledgement does not lock the machine out. Before confirmation the old token remains valid. Offline machines can only be removed, not rotated. A machine cannot remove itself: the owner removes it from another machine.

## Which local process is calling

Agents talk to their local daemon over a Unix socket. The socket directory is mode 0700 and the socket is mode 0600, so only your user (and root) can connect. The daemon then works out which agent session is on the other end, and binds the connection to that session. Later calls use the bound session as the sender. The model cannot name another sender.

The daemon reads the process ID of the caller from the kernel: `SO_PEERCRED` on Linux, `LOCAL_PEERPID` on macOS.

**What a discovered session reports.** The daemon sends the session's folder, its git repository info, mode, model and process ID, and the gateway stores them. Two rules limit that report. A session whose folder is a system temporary folder (`os.TempDir()`, `$TMPDIR`, `/tmp`, `/private/tmp`, `/var/folders/*/*/T`) is still a session but has no project, so a test suite cannot fill the project list. A session whose own process environment has `AGENT_NET_IGNORE=1` is not registered at all: no session row and no project, and a row from before is removed. The daemon reads that environment for Claude Code, for the Pi and OpenCode plugin processes, and for Codex threads from the shared app server they run on. A worker started by the daemon is always registered. The daemon cannot read the environment of another user's process, so such a session is registered normally.

- **Claude Code.** `agent-net mcp` is started by Claude Code for each session. The daemon takes the parent PID of the connecting process and requires a file `~/.claude/sessions/<parent pid>.json` whose `sessionId` equals the session ID the process claims (Claude sets it in `CLAUDE_CODE_SESSION_ID`). If the file is missing or the ID differs, the connection is refused.
  - When `agent-net mcp` switches to a newer binary (see [upgrade.md](upgrade.md#running-sessions)), it keeps its process ID and its parent, so this check gives the same answer. It only executes the file at its own install path, only after that file reports exactly the version the daemon runs, and only if that is a newer `vX.Y.Z` release than its own. Dev builds never switch. The hand-over file (start-up messages and unread input, which can hold tool arguments) is a mode 0600 temp file that the new process deletes at once.
- **Pi.** The Pi extension runs inside the Pi process. The daemon checks that the connecting process has `pi` anywhere in its command line (compared in lower case). This is a weak check. Any process of your user whose command line contains "pi" can claim to be a Pi session, with any session ID. It is accepted because the socket is already limited to your user, and a process running as you can do much more harm than that. A stricter check (matching Pi's executable) is not done yet.
- **Codex.** Codex starts one `agent-net mcp --codex` process per thread, as a child of its shared app server, and does not put the thread in its environment. The process takes the thread from `_meta.threadId` of each tool call (Codex sets it; the model cannot) and says hello once per thread. The daemon accepts a hello only when (1) the process's parent is the app server that the daemon is connected to, whose process ID the daemon reads from the control socket with `SO_PEERCRED` or `LOCAL_PEERPID` (no process name or command line is trusted), and (2) the thread is one the daemon found by listing the server's open threads. If the shared server has just started, the daemon briefly waits for its adapter to connect. For a new thread, it requests a fresh listing and waits briefly for gateway registration; it never registers a claimed ID on the MCP process's word. An embedded terminal Codex does not pass the parent check. What this does not stop: a Codex model that starts `agent-net mcp --codex` through its shell tool, so that the app server is its direct parent, and writes the protocol by hand with another open thread's ID. That needs a command outside the sandbox's process wrapper, and an agent that can run commands as you can also read your files and talk to your socket. It was not closed because a check that the named thread has a tool call running needs the daemon to subscribe to threads, which would also make it a target for approval requests. See [decision 0012](decisions/0012-codex-peers.md).
- **The CLI** (`status`, `list`, `messages`) says it is a CLI client and gets read-only calls, plus the MCP registry and project registry commands.

## Sending as the owner, and who may start workers

A message from the person at a machine is sent from that machine's **owner session** (harness `owner`, shown as `you (on <host>)`). Receiving agents are told it is "from your user in person". So who may send as the owner matters.

- **Only the command line or the TUI.** The daemon accepts `owner_send` only from a socket client that said `client: cli`. An agent connection (Claude, Pi, OpenCode) is bound to a session and is refused. This is the same gate as for the owner-only data (`usage`, `session_text`, `graph_talk`, and the others). The owner session cannot be created by a hello: only the daemon makes it, and the gateway accepts only the native ID `owner` for that harness.
- **Not from inside an agent.** A command-line call is also refused when its process is a session, worker or CTO process of this machine, or a descendant of one. The shared Codex control socket's server is also a root, as are processes whose exact program name and first argument are `codex app-server` or `opencode serve` (including the Codex app's bundled `codex`). The daemon reads the caller's process ID from the socket and walks its parents. CTO process roots are read from atomic snapshots, so a runtime start does not delay this check. An owner terminal launched under one of these servers is also refused; use a terminal outside the server for owner commands. This stops an agent that shells out. It does not stop a process of your user that is not under an agent, which could always use the socket (the socket is yours).
- **What the refusal tells an agent.** The refusal text is fixed. It names the agent-net tool to use and says that a missing tool means old tools, so the agent asks its user to reconnect. It does not give the agent any way around the check. The daemon also records a session's `tools_version` and `tools_old` from the `hello` of its `agent-net mcp` process. They come from the process itself and are only shown (TUI, `agent_session_get`); nothing is allowed or refused because of them. A process can claim any version. The only effect of a false one is a wrong warning or no warning.
- **The sender is never a parameter.** `owner_send` ignores any sender field, and `send` and `reply` use the bound session.
- **Workers.** Any agent may call `agent_worker_start` and `agent_worker_stop` (on its own workers); the person at the command line may stop any worker. A worker cannot start workers: the daemon checks the worker flag of the calling session, which the daemon set from its own table; the gateway checks it again; and a command-line call from inside a worker is refused as above. A machine refuses workers with `setup --no-launch`; the gateway sees that from the machine's hello and refuses early. A machine admits a worker by its recent CPU and memory load and keeps a queue of starts that wait (decision 0018). The settings layer controls the default admission limits, cost estimates and sample count. `workers.isolation_minimum` can require a worktree across the network or for a project; a start without one is refused. The default hard cap is 12 workers on a machine, the queue is as long as agents make it, and a start ends after 2 hours in the queue by default. Network settings can change these limits; a value in the machine's `node.json` wins on that machine. Only the owner of a queued start, or the person at the command line, can cancel it. When a queued start begins, the machine that runs it asks the gateway to pass the task to the owner's machine (`worker.queued_start`, then `worker.task`). The gateway checks that the worker is a session of the sending machine and names that owner. A machine could still make the owner's machine send a task to its own workers, which it could receive from anyone anyway; it cannot make it send to any other session. `worker.cleanup` lists the sessions the machine runs, so the gateway never deletes those records; a machine can only keep its own records that way.
- **OpenCode status checks stay local.** The daemon checks each private OpenCode worker server for active turns. For sessions in OpenCode's shared background service, it reads the local OpenCode `service.json` for the URL and password. It accepts only a loopback HTTP address, does not follow redirects, and confirms that the server has the session before changing its status. The password is used only for these local requests and is not sent to the gateway.
- **Background checkout.** A worktree start chooses a session ID and returns while checkout and harness launch run for at most 10 minutes by default (`workers.background_start_timeout` changes this). The task is sent only after the gateway verifies the registered worker and its owner through the same `worker.queued_start` check used for queued starts. The task send waits up to 2 minutes for a slow gateway. The not-started notice comes from the reserved agent-net sender and is built by the gateway. A machine can report only a start the gateway handed to it (`worker_starts`); the owner, project and harness come from the gateway's record, the notice is sent once, and only the reason is the machine's, cut short, on one line, with agent-net markers neutralized. Another machine, an unknown reference, a closed start or a repeated report makes no notice. A `worker.queued_start` with a reference of another machine, owner or worker is refused. The `retry` flag on a result is set only by the gateway, for its own storage errors. The notice and the change of the start to `not_started` are one transaction. It is not sent to the owner session or to a bot. Only closed starts are pruned, 30 days after they closed.
- **A worker's session is only its own process.** The daemon binds a worker's harness session to the worker's process group. A connection for that session from any other process is refused, so the user's own OpenCode service cannot register or hijack it. A worker has one session. When a resumed worker gets a new native session ID, the gateway keeps its agent-net ID only if the old session is offline and the node, harness and worker ID match.
- **A worker's server** (`opencode serve`) listens on `127.0.0.1` only, with a random password in its environment. Another local user of the machine could reach the port but not the API without the password. The password is stored in a mode 0600 file in the private worker state folder so the daemon can reconnect after a restart. It is not written to the database or the command line.
- **Permissions.** A worker runs with the project's own permission settings. agent-net never passes a flag that turns them off, for any harness. What a worker does when a step needs approval depends on the harness:
  - *OpenCode:* it waits for an answer that nobody gives, and ends at the idle time.
  - *Claude Code:* it is a background session, so it waits on the prompt. You can `claude attach <id>` to answer it. agent-net does not approve for it.
  - *Codex:* the daemon answers every approval request with "decline" and hands back with status `blocked`, with the command, the reason and where to approve it. Codex's own approval policy and sandbox (the project's and your `config.toml`) are not changed by agent-net.
  - *Pi:* Pi has no permission prompts. A Pi worker can run any command its tools allow in the project folder, so the daemon refuses a Pi worker unless it has `isolation: worktree`: it never works in your own checkout.
  - *Antigravity:* workers use `--mode accept-edits --sandbox --settings '{"permissions":{"allow":["command(*)","mcp(agent-net_agent-net/*)"]}}'`. The working folder is the worker's worktree by default. A writable worker also gets its worktree's private git folder and the main repository git folder's `objects`, `refs` and `logs` as `--add-dir` folders, the same narrow set a Codex worker gets, so git can find the repository and its objects and refs: `git status` and `git diff` work, and a commit can write objects and move the branch. It cannot read or change the main git folder's `config` or `hooks`. Without those folders a linked worktree's `.git` file points at the main git folder, which the sandbox hides, so `git status` fails and a read of that folder is denied in headless mode. The daemon still commits worktree changes on hand-back. `accept-edits` permits workspace edits; `command(*)` permits shell commands subject to agy's terminal sandbox; the MCP rule permits the agent-net tools. On agy 1.4.12 the terminal sandbox hides parts of home and allows writes in the working folder and `/tmp`. At worker start, the daemon finds Go on the harness search path, or else in a standard install folder (`~/.local/go/bin`, `~/go/bin`, `/usr/local/go/bin`, `/opt/homebrew/bin`, `/usr/local/bin`), and runs `go env GOROOT GOMODCACHE` with the worker's environment and a five second limit. Each existing folder is passed as `--add-dir`, so agy workers can also write to those toolchain folders. Home itself and its parents are refused, including symlink aliases. The worker's permissions line and daemon start log list the added folders. A missing Go binary or failed probe adds nothing. Read-only workers instead use `--mode plan --sandbox` and only the MCP allow rule, with no command grant. The no-network project level is not enforced. The settings are never saved and no skip-permissions flag is used. A tool that still needs approval is denied in headless mode. agy needs its own writable runtime storage for conversations and helper binaries; the terminal sandbox applies to its commands, not to the harness process itself.
- **Claude Code workspace trust, set by the CTO.** On the owner's instruction the CTO can trust one registered project or one existing folder under the owner's home, on any enrolled machine, with `session_change` action `trust` or `session_start` with `trust: true` (see [decision 0091](decisions/0091-cto-folder-trust-and-standing-orders.md)). This changes who can set trust: before, only the owner at the keyboard. It is audited and undoable. Every use leaves an Inbox notice. The undo removes only trust agent-net added; a folder the owner already trusted is never removed. An undo by path gets the same home check as a trust, so it cannot remove trust outside home; an undo by project uses the registered project. A CTO proposal still cannot set `trust_claude`. The request to another machine is an owner frame that the gateway and the machine both check against the owner session of the sending machine. Trust lets Claude Code load that folder's project settings and hooks, so a trusted folder is as trusted as the code in it.
- **Claude MCP server choice, set by the CTO.** On the owner's instruction the CTO can approve or disable the `.mcp.json` servers Claude Code would load in one registered project or one existing folder under the owner's home, on any enrolled machine, with `session_change` action `mcp_servers` or `session_start` with `mcp_servers` (see [decision 0096](decisions/0096-claude-mcp-approval-and-start-failures.md)). Approving a server lets Claude Code run the command in `.mcp.json` as the owner, in every session that starts in that folder, so an approval is as trusted as the code in the repository. agent-net writes only `enabledMcpjsonServers` or `disabledMcpjsonServers` in the folder's `.claude/settings.local.json`, and only for the names found in the `.mcp.json` files above it. It never sets `enableAllProjectMcpServers`, and a folder with no `.mcp.json` server is refused. A Claude worker or session in a new worktree gets a copy of the owner's own answers for the main checkout (and for the folders above it, including an `enableAllProjectMcpServers: true` there, written as names), so no new approval is made for it; a worker start with `mcp_servers` records the caller's choice for that worker's worktree only ([decision 0101](decisions/0101-claude-worktrees-copy-the-mcp-choice.md)). Every use leaves an Inbox notice that names the servers and what they run, with an undo that removes exactly the names the use added. An undo by path gets the same home check as a choice, so it cannot write a settings file outside home; an undo by project uses the registered project. The request to another machine is an owner frame that the gateway and the machine both check against the owner session of the sending machine; it uses its own frames, so an older gateway or machine refuses it and never runs it as a folder trust.
- **Claude Code workspace trust.** agent-net marks only its own private CTO folder and its Claude worker worktrees as trusted in Claude Code's user configuration. Trust lets Claude Code load project settings for those folders. agent-net removes the trust setting when it deletes a known Claude worker worktree and removes the CTO setting on uninstall. It preserves other project settings and does not mark the user's checkout as trusted.
- **Claude Code workers** start through `claude --bg`, so the session belongs to Claude's background service, not to the daemon. The per-worker settings go on the command line (`--settings`): `crossSessionInbound: "accept"` and the `env` of the session (`AGENT_NET_SOCKET`, `AGENT_NET_WORKER`). Your own `settings.json` is not changed. The worker is bound to the session ID in its session file, and the daemon's process checks use the process ID from that file. Ending is `claude stop`, then the kill of the process group. The stopped session stays in `claude agents` with its conversation until you `claude rm` it.
- **Pi worktree trust:** `--approve` trusts agent-net's own worker worktree for that run, including its Pi settings.
- **Pi workers** run `pi --mode rpc` with a named pipe as standard input that the daemon keeps open, so Pi does not end when the daemon restarts. The daemon chooses the Pi session ID (`--session-id`) before Pi starts and binds it to the worker's process group.
- **Codex workers** stay on their own private app server (not the shared one; see [decision 0012](decisions/0012-codex-peers.md#c4---codex-workers-stay-on-their-private-server)) and run `codex app-server` on `ws://127.0.0.1:<port>`, guarded by a random capability token. The token is in a private file (mode 0600) in the worker folder and never on the command line. Another local user could reach the port, but not the API without the token. The daemon takes messages for a Codex worker from the session that started it and from the person at the machine (the owner), and refuses other agents.
- **Antigravity workers** run a private stream-json process in their own process group. The MCP shim passes `_meta["antigravity.google/root_conversation_id"]` as the native ID. The daemon accepts it only from a process in that worker's group and only when it matches the conversation reported by the worker's `init` event. A connection without a conversation ID can list tools but cannot act as a session. Other conversations and processes outside the group are refused. The shim does not replace the daemon-owned session or its delivery path. Stream output and stderr go to the private worker log; accumulated usage is saved in `agy-usage.json` (mode 0600). Pending last-message text uses the same private idle-notice storage as Codex. The stream reader limits an incomplete event to 16 MiB and reports a larger one as a local failure. Antigravity workers draw on the Antigravity subscription even before a quota reading arrives.
- **Pending idle notices.** A Codex worker whose automatic hand-back fails keeps its last assistant message in the daemon's private database until the idle notice is sent, a new turn begins, or the worker ends. The database is mode 0600. The message can contain secrets and is model output, not an owner instruction. The daemon also keeps the notice's idempotency key so it does not send a second notice after a restart.
- **Reports are model output.** The gateway marks messages from a worker. Claude Code, Pi and OpenCode frame them as the worker's own words, not the user's: no instruction or approval in a report carries your authority. This limits prompt injection through a report; it does not stop a model that decides to obey.
- **Worktrees.** With `isolation: worktree` a worker edits its own git worktree under the daemon's state folder, not your checkout. The branch `agent-net/<worker id>` is made in your project.
- **The feed** now also carries `worker.started`, `worker.stopped` and `worker.refused` (project, harness, model, reason and owner session ID, cut to 200 characters), readable by every enrolled machine, like the other feed events.

## The chief of staff

- **Only the live chief sorts asks.** `agent_ask_sort` (gateway `ask.sort`) is refused unless the caller is the session named by `chief_session`, the session is online, and it runs on the machine that sent the frame. The chief can only act on an ask whose next is the chief. It cannot answer an ask for the owner: `send` hands the ask to a session, and `owner` hands it back.
- **Only the owner or the CTO names the chief.** `chief.set` (operator `chief_set`) needs the owner or CTO actor. The chief must be a live agent session, not a worker, the owner, the CTO or a bot.
- **Nothing is deleted.** Each sort keeps the ask as it was in `ask_sorts`, so a sort can be undone.
- **Only the owner or the CTO undoes a sort or marks an ask.** `ask.undo` (operator `ask_undo`) and `ask.mark` (operator `ask_mark`) need the owner or CTO actor, and the daemon gate for both is the owner gate, so no agent tool calls them. An undo is refused when the ask changed since the sort.

See [decision 0114](decisions/0114-chief-of-staff.md) and [reference](reference.md#the-chief-of-staff-decision-0114).

## Delivery records

Before calling a harness, the daemon saves a delivery intent in its private
`daemon.db`. Confirmed messages and uncertain writes block another injection
of the same message ID. An intent left by a stopped daemon is reported as
failed with `may have arrived: the daemon stopped while delivering`.
A write error after connecting also reports failed, with a may-have-arrived
detail. Neither case counts as confirmed delivery. The record holds the message
ID, state and timestamp, with no message text. It follows the existing private
SQLite permissions and expiry plus seven-day pruning. Pi also remembers its
last 200 accepted message IDs in memory and does not inject a repeated ID.
This lets the daemon repeat a first worker task once within the same durable
delivery when its turn-start ack is missing. The repeated push gets that ack
only after the turn has started. See
[delivery states](reference.md#delivery-states) and [decision 0053](decisions/0053-delivery-records.md).

## Delivery into a Claude Code session

- To deliver, the daemon writes one frame to the inbox socket of the target Claude session. Before it writes, it checks that the path is a real Unix socket (not a symlink) owned by your user.
- The daemon never sends the Claude session token. It does not need it, and it does not read it.
- The message arrives as a user turn, in a `<cross-session-message>` block with the sender's name and ID. The sender's name is cut to letters, digits, `_`, `.` and `-` (40 characters at most). If the message body contains the wrapper tags, they are broken up, so a message cannot close the block early and fake a second one. The marker `[agent-net` is broken up in the body too, so a body cannot imitate agent-net's own details.
- The agent-net details (sender, message ID, session ID) follow the body, as plain text in the same turn.
- Claude's answer comes back on a reply socket (mode 0600, in the daemon's socket folder) for one sender and conversation. The daemon forwards it only when it comes from the inbox address of a Claude session it knows. A reply threaded to a conversation without a parent message is stored by the gateway only when the recipient has written to the sender in that conversation, so a session cannot post into a conversation it is not part of. See [Claude replies](reference.md#claude-replies). An owner message with a conversation ID is stored only when the conversation exists and the recipient is one of its sides; the sender is always the owner session of the daemon, never a parameter.
- Claude Code treats the message as a request from another session. In our tests Claude asks its user before acting on a peer message that is outside the current task (see [step 2 results](step2-e2e.md)). Claude Code's own permission rules still apply to everything the agent does next.

### Accepting peer messages in Claude Code

By default Claude Code holds a cross-session message for your review when the sender's permission mode class differs from the receiver's (for example a prompting session sends to one in bypass mode). Setting `"crossSessionInbound": "accept"` in the user settings turns that check off for all cross-session messages. Then a message from any agent on your agent-net network reaches the session without review, even when the sender runs in a looser permission mode than the receiver. This means a compromised or careless agent on another machine can put text in front of a strict session without you seeing it first.

What stays the same: a peer message is still not your approval. Claude Code's permission rules and prompts still apply to what the agent does next. Claude Code also keeps its stricter sources: a managed (organization) setting or a repository setting of `hold` or `refuse` wins over your `accept`. The accepted values are `accept`, `hold` and `refuse`.

Setup never turns this on by itself. It follows the answer to the one integrations question, or `--accept-peer-messages` (a local answer for that machine). It never changes a value that is already set. It records that it set the value, and `agent-net uninstall` removes the key only if agent-net set it and it is still `"accept"`. See [Integrations](setup.md#integrations).

## Delivery into a Pi session

The extension turns the message into a follow-up message in the running session, marked as coming from another agent and not from you. Pi's own tool permissions apply to what the agent does next.

## Delivery into a Codex thread, and what the daemon can do to Codex

The daemon is a client of the shared Codex app server's control socket (mode 0600 in a 0700 folder, so only your user and root reach it; there is no token). It follows the symlink at `$CODEX_HOME/app-server-control/app-server-control.sock` to its target and requires a socket owned by your user. It only reads thread lists and thread metadata and adds messages to a thread's queue. It never starts, resumes or interrupts an interactive thread, so it is never a subscriber: it receives no approval requests and keeps no thread loaded. A queued message appears in the window as a message from the user; the text says in plain words that it is from another agent and carries no approval. Codex's own approval policy and sandbox still decide what the agent does next. Messages from a worker add the "model output, not your user's words" line.

Setup can set `default_tools_approval_mode = "approve"` for the `agent-net` MCP server only. That lets a Codex thread call the agent-net tools (list, send, reply, start workers) with no prompt, including under approval policy `never`, where Codex would otherwise refuse them. Setup follows the answer to the one integrations question, or `--accept-codex-tools` (a local answer for that machine).

## Integrations answered once for all machines

Your answers to the integrations question ([decision 0021](decisions/0021-network-integrations.md)) are stored at the gateway, and every daemon applies them: it adds or removes the Claude Code hooks and plugin, the Claude peer-message setting and the Codex tool approval on its own machine, at start and every minute.

- **Only you can change them** from a command line or the TUI: `integrations_set` is behind the owner gate, like `session_trust` and `session_policy`.
- **A machine you lose control of can change them for all machines.** The gateway takes the answers from any enrolled machine's token, the same trust as for trust and policy settings. A compromised machine could turn on, for example, accepted peer messages in Claude Code on your other machines. Remove a lost machine at the gateway at once (see [node tokens](#node-tokens)).
- **A machine can say no for itself.** A local `--no-...` answer at `setup` wins over the network answer on that machine, and the daemon never undoes it.
- **Quota-axi reads local logins.** The quota item only lets the daemon run quota-axi on each machine; see [subscription quotas](#subscription-quotas).

## Shared TUI hidden items

The gateway stores session IDs and project node names hidden from the owner's
TUI in `owner_settings`. This preference changes only what the TUI draws; it
does not restrict what an enrolled machine or agent can read through its normal
interfaces.

- **Only the owner can change it locally.** `hidden_get` and `hidden_set` use
  the operator owner gate. The daemon refuses agent clients and command lines
  inside an agent session's process tree, like `session_policy`.
- **Enrolled machines are trusted to write owner settings.** The gateway accepts
  these frames from a machine with a valid node token, as it does for the other
  owner settings. A compromised enrolled machine could change the hidden list
  for every TUI; remove a lost machine at the gateway (see [node tokens](#node-tokens)).
- **Session preferences expire.** A hidden session that returns online keeps
  its preference. The gateway drops the entry after that session has stayed
  offline for 30 days. Project node names remain until the owner unhides them.

Onboarding progress is also stored in gateway owner settings. Its operator
methods use the same owner gate. The CTO restart tool uses the CTO process-tree
check. As with hidden items, an enrolled machine can change the gateway entry.

## Delegation policy

A delegation policy (key `p` in the TUI, [decision 0014](decisions/0014-delegation-policy.md)) is text that agent-net adds to a session's context on every turn: hand code changes to a subagent or a worker, or do them yourself. It is your standing instruction to the session, so who can set it matters.

- **Only you can set it.** The method `session_policy` is behind the owner gate, like `session_trust`: it works from your command line or the TUI, and is refused for an agent client and for a command line that runs inside an agent session's process tree (a program that detaches itself from its parent is not caught). No MCP, Pi or OpenCode tool sets it. A session can read its own policy (the local method `policy`), nothing else. The gateway stores it and logs a `session.policy` event with the session ID, scope, mode and model names. A machine that is enrolled can send the frame, so the gateway trusts the daemons of your machines, as it does for every other frame.
- **A peer cannot imitate it.** The text starts with the marker `[agent-net`, which is broken in every message body ([peer trust](#peer-trust), the marker rule). The model names in it are checked at the gateway and again in the daemon: letters, digits and `. _ : / @ + -` and spaces only, at most 100 characters. No bracket, newline, quote or backtick can get in, so a model name cannot carry an instruction.
- **What it costs.** A policy can make a session start workers (the limits in [who may start workers](#sending-as-the-owner-and-who-may-start-workers) apply: the daemon's limits, the worktree default) or edit less itself. It cannot give a session more permission than its harness already gives it. The session can still ignore it, as with all text.
- **Claude Code files.** With the policy hook (your yes in `setup`), the daemon writes `~/.claude/agents/agent-net-coder-<model>.md` for a `native` policy. Only files with that name prefix and agent-net's marker are changed or removed; a file of yours with the same name is left alone. `uninstall` removes ours. A subagent in that folder can run with the tools of the session: the file does not set `tools`, so it inherits them (Claude Code's default).
- **What the gateway keeps.** A session's own policy lives in its session record and goes when the gateway deletes the old offline session (the retention flags). A project default is a small row in `project_policies` that the retention rules do not delete; clear it in the TUI (`p`, mode none, scope project).
- **The hook adds text, not authority.** The `UserPromptSubmit` hook only prints the context for the prompt. It never blocks a prompt, never changes it, and prints nothing when it has no policy or the daemon does not answer within 700 ms.

## Merge authority

A node's maturity sets its merge authority (decision [0022](decisions/0022-component-graph-and-cto.md)): `full` lets the agent merge and push to the default branch; `delegated` needs an approval from the owner or from anyone above the node in the tree who holds merge (its repo lead or team lead); `ask` needs the owner's written approval. The owner can override it per node with `node_settings_set`, behind the owner gate.

The gateway computes the receiver's effective `merge_authority` at each delivery, including node overrides, and sends it outside the message body. An `auto` edge never widens that setting. The trust note, owner-instruction note and maturity context use the same merge rule. On `full`, the node may merge. On `delegated`, approval must name the exact merge: the owner approves directly or through an agent-net approval note from `agent_owner_ask` with kind `approval`, or the receiver's repo lead or team lead approves in a message from them. On `ask`, always ask for the owner's written approval of the exact action. Workers put the action and recommendation in their report when they cannot request approval. The owner-instruction note carries the owner's own words and points to the receiver's merge rule; that rule still decides who may merge. A body claiming approval does not count.

This is **guidance plus approval objects, not a git lock**. `MergeApprovalFor` returns the authority, whether approval is required, whether it must be written, the delegates and the text the session gets. Nothing in agent-net refuses a push or blocks a merge in git, and an agent can ignore the text. Written approvals arrive in agent-net's own approval note for one exact action, through the owner's inbox. This text change adds no grants and does not infer merge authority from owner excerpts. Stricter repository rules and harness permissions still apply.

A critical repository should also have branch protection on its default branch: require a pull request, require review, and stop force-pushes. Branch protection is enforced by the repository host; agent-net's merge authority is not.

### Delivery steps and evidence

A daemon may close its own machine's open change after observing an external merge or an abandoned branch; `change.reconcile` is not an agent or owner socket method. The daemon requires commits beyond a known base for an external merge. The gateway rejects an external merge whose head equals its base, binds observations to the current head and revision, and requires an age over seven days and no evidence for abandonment.

The delivery pipeline (decision [0057](decisions/0057-delivery-pipeline.md)) records each step's result as evidence. The gateway stores changes, their revisions and evidence in `gateway.db`, and routes ready node-owned steps to that node's sessions. The tools `agent_change_submit`, `agent_step_claim`, `agent_step_report` and `agent_changes_find` go through the daemon to the gateway. The pipeline settings cutover and owner controls are available. No merge is refused in git. A worker's complete hand-back submits its change from the worker's machine, with the worker's caller as the change's session; the gateway accepts it only when that session is on the machine, or the worker's session is on the machine and that session started it. A change ID passed on by a continue comes only from the machine's own worker record, never from a frame.

Review is required for hard or very_complex changes, a judge risk of at least
4 (or a lower configured threshold), or sensitive paths in the daemon's diff.
Sensitive paths include migrations, authority and token files, security files,
file transfer code, the installer and workflows. The daemon sends an optional
risk score and sensitive-path reason; older peers may omit them. The gateway
keeps recorded triggers on later submissions so omitted fields cannot erase a
review requirement. A missing diff at hand-back or session submission requires
review. Sensitive names in directory components also require review, with
case-insensitive matching. These rules apply to pipeline evidence; git itself still allows direct merges.

A new daemon checks merge ancestry in the target repository before reporting a
merge pass. It sends an optional `merge_contains_head` result for the exact
reported head and merged commit. A negative result is refused by the gateway
before evidence is stored. Missing commits, non-commit objects and failed git
checks count as negative. Daemons from version 0.4.131 and dev builds must send
proof; the gateway reads their version from the authenticated machine's hello.
A positive result cannot bypass required steps or a head change. Older daemons omit the proof and retain the existing checks.

- **A machine speaks only for its own sessions.** A submit names a session on the sending machine, or the hand-back of a worker on that machine which the session started. A session submits only its own work. A step report comes from a session on the sending machine. `done_by` names a worker that session started, or `owner` for a direct owner approval of a delegated step.
- **The owner reads every change.** The TUI reads changes with the daemon method `changes_owner_find`, which is read only and follows the same gate as the other owner reads: only the owner's command line or TUI, not an agent connection and not a command line that runs inside an agent session. Agents keep `changes_find`, and a TUI connection cannot call it or the write methods.
- **Evidence is append-only.** The gateway never changes or deletes a stored piece of evidence; database triggers refuse an update or a delete.
- **The daemon fills in who and what.** The session that submits or reports, its project and its model come from the daemon, never from the arguments. A submit takes the head commit of the branch from git (a given commit must be on the branch) and says whether the worktree that has the branch checked out has changes that are not committed. A submit for another project than the session's is refused. Workers cannot submit or report; they can only read changes.
- **`done_by` names a worker you started, or the owner of a delegated approval.** For a worker, the daemon asks the gateway for the session's record and refuses the report unless its caller is the reporting session, on any machine. For direct owner approval under `delegated`, only the caller or an allowed delegate may report `done_by: "owner"`. Under `ask`, the owner must answer the Inbox item instead.

- **Delivery git calls do not trust worker-writable config.** A worker can edit the config of its repository, and all worktrees of a repository share one `.git/config`. Git runs what that config names: filter drivers on checkout and status, merge drivers during a merge, `core.sshCommand`, `core.askPass` and credential helpers during fetch and push, and the receiving side's hooks when the push is redirected to another local repository. A check of the config cannot close the gap between the check and the git call that reads it, so the merge step does not depend on one:
  - **Merge works in a mirror the daemon owns.** `Merge` takes `MirrorDir`, a bare repository it creates and marks as its own (a directory with other content is refused: `ErrNotOwnedMirror`). It fetches the target branch from the pinned remote URL and the change branch from `RepoDir` by name, resets and merges in a worktree of the mirror, and pushes to the pinned URL. It never checks out, merges or pushes in a repository a worker writes. The only calls that touch `RepoDir` are the `ls-remote` and the fetch of the change branch, which run `git upload-pack` there (no filter, merge driver or hook), and `RepoDir` is not changed.
  - **The remote URL is pinned.** `MergeRequest.RemoteURL` and `WatchRequest.RemoteURL` are required. They come from the caller's own record (the project registry or the first URL it saw), never from the repository. Fetch and push use that URL, not the remote's name, so `remote.<name>.url`, `pushurl` and `url.*.insteadOf` in the repository are not used. The step also refuses (`ErrRemoteURL`) when the repository's own fetch URL for the remote differs, as a sign of tampering.
  - **A pre-flight check refuses unsafe repository config.** Before it uses a repository, every delivery step lists the config with its scope (`git config --list --show-scope --includes`, which covers `config.worktree` and included files) and refuses with `ErrUnsafeConfig`, naming the key, when a repository-level value is one of: `filter.*.smudge|clean|process`, `merge.*.driver`, `diff.external`, `diff.*.command|textconv`, `core.sshCommand|askPass|gitProxy|worktree`, `url.*.insteadOf|pushInsteadOf`, `credential.*`, `gpg.program`, `gpg.*.program`, `protocol.*`, `lfs.*`, `remote.*.vcs|uploadpack|receivepack|proxy`, `submodule.*.update` that starts with `!`, and the `http.*` keys that change where git connects or what it trusts (`proxy`, `extraHeader`, `sslVerify`, `sslCAInfo`, `sslCAPath`, `curloptResolve`, `followRedirects`, `cookieFile`, and similar). Harmless `http.*` keys such as `http.version` pass. Merge runs it on `RepoDir`, on the mirror (before the work and again before the push) and on the merge worktree (its `config.worktree`). `core.worktree` is refused because it points git at another working directory, which made clean-tree evidence false. `protocol.ext.allow=never` is passed with `-c` on every call, which beats every config file.
  - **The owner's system and global git config stay trusted.** A worker cannot write them, and a push over https needs the owner's credential helper. `.gitattributes` in the tree can only pick a driver by name, so it reaches only drivers the owner defined globally.
  - **Limits.** A repository that needs a local filter (for example LFS set with `git lfs install --local`) is refused until the filter moves to the global config. `RunStep` and `NewCommits` still read the repository the caller gives them, so the check there has a window of one git call; `RunStep` runs the repository's own command anyway, and the watcher repository should be a clone the daemon owns. A `.git` file that points at another git directory is not checked.
- **Evidence names one exact commit.** A commit must be a full 40 or 64 character lowercase hash; a short hash or a ref name is refused. A report for a commit that is not the change's head (or, after merge, its merged commit), or for an older revision, is refused (`Pipeline.Accepts`). Evidence of an older revision does not count.
- **The reporter cannot set the independence field.** Agent-net derives it from the reported doer's session and model maker, compared with the change's author. A report that sets it is refused. The gateway verifies that a `done_by` worker was started by the reporter, but does not verify that the worker actually did the step. A reporter can therefore attribute its own work to that worker and make the evidence appear independent. Step 3 must close this gap before enforcing independence.
- **Evidence is never a message.** Only an authenticated step report, an answered Inbox item or a GitHub result counts. A message body, a trust note or a CTO note does not.
- **Limits on a report:** at most 16 fields; field names of lowercase letters, digits and `_`, at most 40; each value at most 200 bytes with no control characters; a note at most 8 KiB; a branch name at most 255 bytes; a change title at most 200 bytes.
- **Diff summaries:** the gateway stores a diff stat of at most 8 KiB for a revision. It can name files in the change and is sent to the step owner's sessions. Control characters other than tabs and newlines are refused, and agent-net markers are neutralized in task text.

## What a worker can do: the sandbox level

A worker is an agent that nobody watches, so agent-net does not let it ask for approval. The daemon declines approvals where the harness supports them. The worker permission level is set per project in the TUI (`p`, line Workers) or the settings layer and applies to that project on every machine. Codex uses its own sandbox; Antigravity uses its own `--sandbox` flag. On Linux, Pi and OpenCode workers of a project that has the OS sandbox on run in bubblewrap ([below](#the-os-sandbox-for-pi-and-opencode-workers)). Otherwise Claude Code, OpenCode and Pi workers run with the owner's permissions. The owner-only `session_policy` method prevents an agent from widening a project's level. When no level is set, the node's maturity default applies ([decision 0022](decisions/0022-component-graph-and-cto.md)): `experimental`, `early`, `growing` and `mature` give `sandboxed`, and a node with no maturity keeps `sandboxed`. The owner can override it with `node_settings_set worker_sandbox`. Both the policy form and node setting change the same repo value. At cutover, the strictest machine policy and old node fallback win. An existing repo value is tightened only if its effective value would otherwise be looser than the old fallback or a machine policy. Inbox checks name the machines made stricter and their old values, and report case-ambiguous project names.

The level definitions and limits below describe Codex's sandbox. Antigravity has separate behavior described in [Antigravity workers](antigravity.md#workers).

| Level | Writes | Reads | Network |
|---|---|---|---|
| `sandboxed` (default) | Only its worktree, the temp folders, and `objects`, `refs`, `logs` and `worktrees/<name>` of the main repository's git dir | Anything your user can read | on |
| `no-network` | The same | The same | off |
| `read-only` | Nothing | The same | on |

- **Reads are not limited.** A sandboxed worker can read any file your user can read: `~/.ssh`, `~/.aws`, browser profiles, other projects. Treat what a worker can read as what it can leak.
- **With the network on, it can send what it reads.** `sandboxed` and `read-only` allow network access, so a worker that reads a secret (or a prompt-injected worker) can send it out. `no-network` blocks this. Use it for projects with secrets next to the code, or for tasks that need no network. The cost: no package installs, no `git fetch`, no web calls.
- **Writes are limited, but a commit is a write to your repository.** The worker can create objects, move refs (branches and tags) and write reflogs in the main repository, and change its own worktree's index and HEAD. It cannot change `config` or `hooks` of the main repository (Codex keeps `.git` read-only, and agent-net grants only the folders above), so a worker cannot plant a hook that runs outside the sandbox the next time you use git. It can still make commits on branches other than its own, and delete refs. The branch `agent-net/<worker-id>` is its own; read the diff before you merge.
- **Linked worktrees.** A Codex worker in a linked worktree, including with `isolation: none`, can write that worktree's private git directory and the common git directory's `objects`, `refs` and `logs` folders. These entries let it commit without granting write access to the common git directory as a whole.
- **No approvals means no way to ask.** A step the sandbox refuses fails inside the worker. It may report that it is blocked, or work around it. The report is model output: check it.
- **The agent-net tools are off for a Codex worker.** Its private app server runs with `mcp_servers.agent-net.enabled=false` when your Codex config defines the server, so it cannot list, message or start anything on the network. A server defined only in a project's `.codex/config.toml` is not turned off. Other MCP servers stay available to it, with their own authority.
- **The sandbox is Codex's.** Its strength is the strength of Codex's Linux (bubblewrap/seccomp) and macOS (Seatbelt) sandbox on that machine. agent-net chooses the policy and does not test the isolation itself.

### The OS sandbox for Pi and OpenCode workers

An optional wrapper around the whole harness process, so shell commands, language servers and MCP servers of the worker are inside it too. It is off by default. Turn it on per project in `node.json` on the machine that runs the workers: `"workers": {"os_sandbox": ["shop"]}` (`"*"` is every project), then restart the daemon. It does not change the level; it applies it. Design and reasons: [docs/design/worker-os-sandbox.md](design/worker-os-sandbox.md).

On Linux it is bubblewrap. A Pi or OpenCode worker of such a project:

- sees the host read-only, with new user, PID, IPC, UTS, cgroup and (for `no-network`) network namespaces, a new `/proc` and a new `/dev`;
- can write only its worktree, the main repository's `objects`, `refs`, `logs` and `worktrees/<name>`, its own private state, and a private `/tmp` (the `tmp` folder in its worker folder). At `read-only` the worktree and the git folders are read-only too. The worktree's `.git` file stays read-only;
- cannot reach `$XDG_RUNTIME_DIR` (the systemd user bus, which can start processes outside the sandbox, and the user's agent and keyring sockets), the sockets in the host's `/tmp` (the Claude Code and Codex background services, tmux, ssh-agent), or `/run/docker.sock`. The agent-net socket is bound back so the worker's plugin or extension works;
- cannot see, signal or trace your other processes.

Pi gets a private agent folder (`pi-agent` in the worker folder, set as `PI_CODING_AGENT_DIR`) with a copy of `auth.json` and `models-store.json`; every other entry of your agent folder is bound into it read-only. Pi runs an `auth.json` key that starts with `!` as a shell command and loads code from `extensions`, `npm`, `git` and `settings.json`, so a worker that could write your real folder could plant a command for your next, unsandboxed Pi session. Its session folder stays writable, because the daemon reads the worker's usage there. Token refreshes stay in the copy. OpenCode keeps its private data and state folders writable; `~/.config/opencode` and `~/.cache/opencode` are read-only.

Fail closed: with the switch on, the daemon refuses the start, and says why, when bubblewrap is not installed, when a probe sandbox cannot be made (user namespaces turned off), on macOS (not built yet), for a `no-network` Pi or OpenCode worker (the wrapper would also cut the harness's connection to its model), and for a Claude worker (`claude --bg` runs the session in Claude Code's own background service, which no wrapper can reach). It never starts such a worker without the sandbox. Codex and Antigravity workers keep their own sandboxes and are not wrapped.

What it does not cover:

- **Reads.** As above: the worker can read every file you can read.
- **Exfiltration and local services.** With the network on, it can send what it reads and reach any service on localhost or the LAN. An ssh server on this machine that accepts a key the worker can read is a way out, and so is any local HTTP API that runs commands.
- **Abstract Unix sockets** of the host stay reachable while the network is shared (for example an X11 display), and so do sockets on the filesystem outside the hidden folders, because a read-only mount does not stop `connect()`.
- **Its own state.** It can change its own session history and its private copy of the credentials.
- **Kernel and bubblewrap flaws.**

MCP servers that the harness starts are inside the sandbox too. One that needs a hidden socket or a write outside the sandbox fails there: for example an MCP server started with `docker run` cannot reach the Docker socket.

There is no `--die-with-parent`: a worker outlives a daemon restart. The daemon stops it with SIGTERM, then SIGKILL, to bubblewrap's process group, which holds every process of the sandbox.

## Peer trust

Requests use rules on graph edges. `normal` uses the normal peer guidance, `auto` adds the trust note, `ask` holds the request for the owner, and `never` refuses it. The sender-specific edge takes precedence over the wildcard edge into the recipient node. The `t` key cycles the wildcard through `normal`, `ask` and `auto`.

The new delivery assignment edges, including `delivery_deploys`, do not set
request permissions and cannot override those rules. The existing `deploys`
edge retains its request rule and supplies the default deployer. The owner or CTO may change or undo a step owner edge, including `deploys`.
A node-owned task can be claimed or reported only by an online non-worker
session of that node. A claim reserves the task; the first report can also
claim it in the evidence transaction.

The risk of `auto` is that text relayed by a peer can steer a session without the owner seeing it first. The trust note follows the receiving node's merge authority; an `auto` edge does not approve merges on a `delegated` or `ask` node. It is guidance to the model, not a replacement for harness permission rules. It still asks for owner approval before deleting data, spending money, or sharing or using secrets that can spend money. This is the owner's list of 2026-11-08. Everything that was on the older, longer list (force-pushing, rewriting history, changing credentials or permissions, deploying beyond the brief) follows the normal rules, the receiving node's settings and the harness permission rules.

Only the owner can change edge rules or answer a held request through the local operator interface. `edge_set`, `session_trust`, `held_list` and `held_answer` are behind the owner gate. The gateway trusts enrolled daemons, so a compromised enrolled machine can send these gateway frames. Remove a lost machine at the gateway.

The gateway supplies the trust and approval flags; senders cannot set them in a message. Adapters write the notes after the body. The marker rule breaks `[agent-net` in a body so that it cannot imitate agent-net framing. Plain text can still claim approval, so only the agent-net note proves it. An approval applies to the held request. A decline or expiry generates a reply to the sender. Rules and held messages are stored at the gateway; a session removal does not remove a graph edge.

## Cited message notes

A message may cite another message by its ID, and agent-net then adds a note with the cited message's sender, recipient, time and body ([decision 0121](decisions/0121-cited-message-notes.md)). This does not widen who can read a message. The gateway adds the note only when the sender of the new message was the sender or the recipient of the cited message. That session could already read the text and paste it into its own message; agent-net only vouches that the text is genuine. For any other ID, whether it exists or not, the note is not added and the reply is the same, so a session learns nothing about messages it was not part of. The gateway looks at the first 3 distinct IDs of a body only, and shows at most 1500 characters of each cited body. It writes the notes itself on every delivery and every `message_status` read, and ignores any `cited_notes` a sender supplies. Names and bodies in a note pass the marker rule. The direct read of a message (`message_status`, `agent_message_get`) still checks that the caller took part in it.

## Hints about who can help

After a failed tool call, agent-net can add one line telling the model which other online session may own the failing service (decision [0013](decisions/0013-peer-trust-and-hints.md#part-b---hints-about-who-can-help)). The matching runs in your daemon, on tokens, with no model call. What leaves the machine: nothing new. The daemon asks the gateway for the list of online sessions, which it already knows, and compares the error text with it locally. The error text is not sent to the gateway.

- The hint is advice. The model may ignore it, and it only suggests that the model asks with `agent_message_send`. A suggestion names a session by its name and ID and says why it matched (a project, repository, folder or host name found in the error, or words of the session's own `expertise`, function and task). A session name is its project identity or a name the owner chose, and only the owner renames a session (decision [0033](decisions/0033-session-names-are-owner-only.md)), so a session cannot pick its name to attract hints. It writes its own `expertise`, function and task, so it could choose those words. The worst result is a message to that session, which is still a normal message under all the limits.
- The Claude Code hook is added to `~/.claude/settings.json` only with your consent, and removed by `agent-net uninstall`. The command reads the hook input on stdin, talks to the local daemon only, gives up after 280 ms and prints nothing on any error. The hook runs inside the Claude Code session's process tree, so it uses the `hint` method with the session ID Claude gives it. That method can only use up the rate limit of that session; it is not an owner-only method.
- Limits: at most two suggestions; the same suggestion for the same session at most once per 30 minutes; any hint for a session at most once per 45 seconds; never the session itself, an offline session, the owner, a worker or a session of the same project.
- The `expertise` line is stored at the gateway with the profile, cut to 200 characters, with control characters removed.

## Desk (tab 7)

The Desk (tab 7) excludes worker sessions. The daemon refuses a worker's explicit `agent_owner_ask` request; workers should use their hand-back or `agent_message_reply`. The section shows what another session waits for: a permission, a question or input (decision [0016](decisions/0016-needs-you.md)). The state is `kind`, `text` and `since` on the session. The daemon sends it to the gateway with the session, and every enrolled machine's TUI can read it: the same audience as the agent list, not only the session's own machine.

- **What leaves the machine.** The text comes from the harness: a Claude Code permission request (the tool name and its command, path or URL), an `AskUserQuestion` question, or notification, an OpenCode permission or form title, or the agent's own `waiting` line. Claude Code's own session file says that a dialog blocks the turn; the daemon then reads the transcript for the one tool call that has no result yet and keeps only its name and short summary or question, never other transcript text (decision [0060](decisions/0060-waiting-for-user-auto-answer.md)). Codex thread flags carry no text. The line is cleaned of common secret shapes (`[REDACTED]`), cut to 200 characters, turned into one line and stripped of control characters at the daemon and again at the gateway. A permission request can name a file, a command or a URL, so treat the line as metadata that every enrolled machine's TUI can see. Do not put secrets in a question you ask an agent to announce.
- **Who sees the question.** The owner's TUI and CLI only. Agent reads (`agent_sessions_find`, `agent_session_get`, the CTO's views) show that a session waits, its `kind` and `since`, never the question; the canary test checks this.
- **The notice to senders.** A request delivered to a waiting session makes the gateway store one notice from agent-net to the sender, at most once per sender per waiting period (the key names the recipient, the start of the wait and the sender, so a restart does not repeat it). The notice quotes neither the question nor the request. It is never sent for a notification, a reply, agent-net's own messages (so it cannot loop) or the owner. A bot gets the same words in its send receipt, not a stored notice. The session name in it is cleaned and its markers are neutralized, so a session name cannot forge agent-net's framing. A session that lies about waiting only makes its senders get one notice each.
- **Who may set it.** A plugin is bound to its session by the local socket. A command line hook names `harness` and `native_id`, and the daemon silently ignores an unknown session. Like `hint`, the `waiting` method is not owner-only: it must work inside a harness turn. Any process of your user can call it for a session the daemon knows, which is no more than that process can already do.
- **What it changes.** An explicit ask also causes a desktop notification on machines with an owner TUI open, at most once per session per five minutes. The notification contains no question text. A session can lie about waiting, just as it can lie in its announced task. The owner can turn notifications off on each machine. Reading or changing that setting uses the owner-only gate and the private CTO channel. A wait carries no authority and clears when the session works again or you answer.
- **The hooks.** The Claude Code hooks are added only with your consent (the one integrations question, or `--claude-needs-hook`), removed by `agent-net uninstall`, talk to the local daemon only, give up after 300 ms and print nothing on any error. They run inside the session's process tree and use the session ID Claude gives them. After an upgrade the daemon updates them at start, only when the answer is on, agent-net added them and they differ; it never adds them for a binary whose name does not start with `agent-net`, because those hooks could not be told from yours.

## Guidance text is advice, not enforcement

`agent-net setup` adds a short block to `~/.claude/CLAUDE.md` and `~/.pi/agent/AGENTS.md`. It tells the model to answer peers directly, to check with you before risky or out-of-scope work, and to never take a peer message as your approval, unless the note that agent-net itself adds says you allowed this session to act on peer requests (see [peer trust](#peer-trust)). It also tells the model to relay a request you asked it to send, and let the receiver decide, and to ask a peer early when an error comes from something another session owns. This is a request to the model. It can be ignored, or overridden by a persuasive message. The things that actually limit what an agent can do are the harness permission rules (Claude Code's permission modes and allow lists, Pi's tool settings). Keep those on. A peer message is never your approval, except through peer trust, which only you switch on, per session. Do not configure your agents to treat peer messages as approval in any other way.

## CTO tool classes and the two rules

**Rule 1: changes.** The per-tool and per-action class table applies CTO
changes at once except for `node_remove`, `agent_tombstone`, `repo_archive`,
`machine_remove` and `machine_rotate`. Those five remain proposals approved by
the owner in a dialog or the Inbox. Unknown tools and actions fail closed.
Concrete handlers keep their schema, audit and integrity checks. The owner sees
an Inbox notice after an immediate action, with a diff and undo where available.
The old graph window no longer changes the class of an action.

The CTO may send as owner at once using `as_owner`; the daemon attaches the CTO
owner-instruction note. Other sends carry the CTO peer note. The gateway accepts
that flag only from the registered CTO session and trusts enrolled daemons to
supply it honestly.

**Rule 2: reads.** CTO data views reuse ordinary agent dispatch with the CTO
caller; views without an ordinary source are removed. Static charter guidance
and the CTO's own timeline remain. The parity and canary tests enforce this
for those views. Immediate actions also return their concrete results to the
CTO, which can include paths or data returned by an MCP server. Proposal outcome
notes carry no private result.
The CTO now uses its registered network session for these checks. An agent
session cannot call the owner-gated `owner_send` method.
The gateway refuses a new agent conversation addressed to the CTO. It accepts owner messages, agent-net system notices, and replies in conversations the CTO started, including later replies. This rule applies to workers, merger and orchestrator sessions too.

## Limits against loops

CTO proposals are stored in the hosting daemon's private state database.
The owner gate protects proposal listing and decisions, including relayed
requests. An enrolled daemon is trusted to enforce that gate before relaying,
as it is for other owner operations. A proposed call is checked before it is
stored and again before it runs. Only the five actions listed above take this
path. Only the
daemon's runner executes an approved call, with actor
`owner (CTO proposal p_N)`. For a dialog answer the daemon accepts only the private CTO MCP connection, and checks its process tree again on each call. The MCP server sends an answer only after the harness returns an elicitation response; the model has no approval tool. The dialog text comes from the stored proposal. The daemon wraps ordinary tool results separately so their contents cannot masquerade as a proposal. This relies on the harness presenting elicitation to the owner, and on the existing local process checks described above. The model cannot approve its own proposals through the tool interface. Identical open calls are refused, open proposals expire after 24
hours, and at most 20 may be open on the hosting daemon. Outcome notes contain
only the status or a bounded error, never a successful call's result.
A CTO project clone accepts only `git@host:owner/repo.git` and
`https://host/owner/repo.git` remotes. It refuses local and file URLs, reserves
a previously absent destination, disables file and external Git transports,
and times out after 45 seconds. The destination daemon repeats the remote
and home-folder checks before cloning. The test-only clone hook is not set in
production.

The CTO's advisory placement memory is stored
in the private state database. Purpose and placement are each limited to 512
bytes, and a read returns at most 100 entries. It does not authorize a change.

The daemon retains only sender names and times for the last three messages it
delivered to the CTO, and snapshots that metadata with each proposal. It does
not copy their bodies. An approve-all confirmation names the calls being
approved; the daemon rejects the request if the open list has changed.

The gateway enforces these on every send:

| Limit | Value |
|---|---|
| Message body | 64 KiB |
| Rate | 20 messages per minute per sending session |
| Messages in one conversation | 50 |
| Reply depth ("hops") | 8 |
| Message lifetime | 24 hours, then `expired` |
| Delivery retries | Known temporary failures: 30 seconds, 2 minutes, 10 minutes, then every 30 minutes until expiry; uncertain writes are never retried |

A repeated send with the same idempotency key stores nothing new.

## MCP server registry

Registered MCP servers run on the machine that hosts them, as your user. Scoping, import and native removal are in [Skills and MCP management](#skills-and-mcp-management).

## Skills and MCP management

This section covers everything the owner can do with skills and MCP servers across machines ([decision 0027](decisions/0027-skills-and-mcp-management.md), [reference](reference.md#tools-inventory)): see them, import them, assign them, remove them and undo that. Skill files and MCP configs can hold secrets and can steer an agent. Each rule below is enforced in code, not only in guidance text.

### Who may change things

- **Shared MCP server administration.** The daemon refuses `mcp_add` and `mcp_remove` from bound agent sessions. The gateway also refuses add or remove requests with non-empty `added_by_session` attribution. The owner's TUI and CLI keep working. A CTO `mcp_change` runs at once through the owner CLI path. Agent sessions may still list and test shared servers. Plugin calls to add or remove a shared server reach the same daemon check and are refused.
- **The owner gate.** `tools_import`, `tools_save_changed`, `tools_update_plan`, `tools_update_copies`, `tools_plan`, `tools_apply`, `tools_undo`, `tools_history`, `mcp_import`, `mcp_native_remove`, `mcp_coverage_plan`, `mcp_coverage_apply` and `mcp_coverage_undo` are owner methods. They work from your command line or the TUI. The daemon refuses an agent client and a command line that runs inside an agent session's process tree. The private CTO cannot read tool inventory or metadata outside ordinary agent tools. Its plan, apply, undo, import and native-removal calls execute at once. A plan can be limited to chosen machines (`target.machines`). This adds no method and no new caller: it goes through the same gate, the gateway checks that each machine is enrolled, and a daemon ignores any target that names another machine.
- **The gateway checks too.** It accepts these requests only from the online owner session of the sending machine. As with every other frame, the gateway trusts the daemons of enrolled machines (see [the trust limit](#the-trust-limit)).
- **Advice is not authority.** A charter's `rules`, a peer message, a skill's text or a project file never grants a change. Rules are free text and are never turned into tool assignments.
- **Audit.** The gateway records each plan and apply with the actor (owner, CTO, or owner with a CTO proposal ID), the targets and the result. Records can hold machine and file paths. They never hold skill contents or MCP credential values.

### What discovery sees

The daemon scans its own machine at start, after it reconnects and every five minutes. It never runs a skill or a server. It sends the gateway names, paths, hashes and plain reasons, not raw MCP configuration. The one skill text field sent is its front-matter description, cleaned of control characters, with whitespace collapsed and cut to 1,024 characters on a word boundary. This lets the Tools tab explain a skill and estimate its session token cost. No other skill contents are sent by the scan. MCP fingerprints leave out environment and header values, URL queries and unknown argument values. Each skill manifest contains relative file names, sizes, per-file fingerprints and the newest modification time, but no file contents. File names and fingerprints can reveal information about installed skills and are visible wherever inventory is readable. Scan limits:

- Skill-entry symlinks are followed only when their resolved folder is inside the home. The scanner opens the folder within the home root and rechecks the target. A separate comparison fingerprint omits executable bits and common editor noise; integrity operations use the strict hash.
- A comparison manifest: at most 200 files and 64 KiB of JSON per skill copy. An oversized manifest is marked too large to compare; no partial file list is sent.
- A prompt or agent file (a Claude Code command or subagent, one Markdown file): at most 64 MiB, read through the opened folder root, so a link out of the home or a file that changes while it is read is refused. The scan sends the same fields as for a skill: name, path, hash, front-matter description and a one-entry manifest. Import packs the file as a one-file bundle and the gateway checks it against the observed hash with the same 2 MiB limit.
- A skill bundle: at most 10,000 entries and 64 MiB of regular file data. Special files, absolute or escaping symlinks and files that change while being read are rejected.
- Pi source discovery: at most 10,000 directories and 32 path components.
- MCP and settings files: 1 MiB. A report: 10,000 entries, 512 KiB of field text, 1 MiB of manifests and 3 MiB encoded. A single file list is limited to 256 KiB. Reads are paged to 240 KiB.
- An oversized source or report fails the scan. It is never silently cut. A scan that fails for one source keeps the other fresh observations and marks only that source stale. A scan that produces no successful observation leaves the old records marked stale.
- The owner-only `tools_skill_details` reads the file manifest of one observed skill on one machine. The gateway checks the owner session; the machine checks that the name, location and hash are in its current inventory before it reads the folder, and it checks the hash again before answering. It sends file names, sizes, fingerprints and the newest modification time, never file contents. An agent session cannot call it.

### The private skill store

`tools_import` copies a skill from a machine to the gateway. Only an owner connection can ask for it. The gateway copies skill contents as supplied and does not scan them for secrets. The owner is responsible for keeping secrets out of skills before importing them.

- The gateway checks the observed hash, so a skill that changed after its scan is rejected. It rejects traversal, escaping links, special files and bundles over 2 MiB or 2,048 entries.
- The skill goes into `skills.git` in the gateway data directory: a private bare Git repository, mode 0700. SQLite keeps the item ID, content hash, commit and source. Nothing is pushed anywhere.
- Git keeps history. A later version that drops a secret does not erase it from earlier commits. Anyone who can read the data directory or a backup can read these contents.
- `gateway_backup` holds a consistent database snapshot, the skill repository, the CA key and the server key. The folder and its files are owner-only. Treat a backup like the gateway data. Restore writes only to a new directory and checks SQLite, Git objects and every saved skill hash before it finishes.

### Plans, copies and ownership

Project skill switches and the new-projects setting use the owner gate at both
the operator socket and gateway. The CTO can change them at once through the owner path. An unmanaged switch imports first,
using the same bundle, hash, size and secret checks. Its assignment names only
the machines with observed copies, so migration cannot deploy it to a new
machine. Plugin, package and system sources remain read-only.

The gateway may create and apply a skill plan with actor `standing choice`
when it first learns a project. This only adds the new project to an existing
project assignment whose owner set new-projects on. It keeps the assignment's
machine limits, content version and existing off choices. It cannot import a
new version, turn an existing project on, or broaden the machine list. Global
items need no automatic plan. The ordinary snapshot, conflict, backup, journal,
reconciliation and undo rules still apply. Incoming daemon frames cannot claim
the `standing choice` actor. The setting defaults on for existing global skills
and off for other assignments. Failed automatic plans leave the new project
eligible for retry on a later discovery report.

A plan changes nothing. It lists the machines, harnesses, checkouts, sources, destinations and conflicts, and binds to the inventory, the known projects, the item versions and the desired revisions. An out-of-date plan is refused. Destination checks use a bounded fingerprint, locally keyed, so the plan carries no file contents. A fresh partial scan blocks the failed skill name and overlapping source folders,
including entry aliases, rather than every skill on that machine. A failed
source root with unknown contents still blocks skills it could hide. Missing
reports, reports older than 15 minutes, scanner failures, failed upload
acknowledgements, report-limit failures and scans with no successful items
block the machine. The gateway checks these conditions again on apply; the
daemon also checks them before verifying absence. Specific refusal reasons
survive inventory merging and reach the owner without file contents. Opaque
sources and offline paths remain conflicts. An apply records the desired state as pending. It does not claim that a file changed. A target stays `pending` until the machine reports.

When a daemon applies a target:

- It fetches the stored bundle and checks its hash before it writes.
- The daemon anchors skill copy and removal paths in Go `os.Root` directories on Linux and macOS. Opened roots stay anchored when a parent is moved and refuse symlink escapes. Git index locks are created exclusively through the opened Git root.
- It stages the copy beside the destination and renames it into place without replacing anything. A different existing destination is a conflict. A symlinked parent is refused.
- It records path, hash, revision and whether it created the folder in the owner-only `skill-manifest.json`. A matching folder that was already there is recorded as not owned and never removed.
- A prompt or agent copy is one file in `.claude/commands` or `.claude/agents` of the user folder or of a checkout, written through the same opened-root, journal, Git exclude, no-replace rename and backup steps. The daemon refuses any other root for these kinds, whatever the plan says, and refuses to overwrite a file whose content differs. Its Git exclude line names the file without a trailing slash. Removing a tracked project command or agent through a branch is not supported.
- It places project copies in `.claude/skills` and `.agents/skills`, and global copies in `~/.claude/skills`, `~/.agents/skills` and, only if `~/.gemini` exists, `~/.gemini/config/skills`. It refuses other sources that may shadow these paths, and refuses to remove the last copy of a skill that was never imported. Project `.agent/skills`, system and plugin skills are observed without being managed.
- Before a project copy it refuses the home folder, a folder that is not the top of a Git working tree, and a linked worktree that is not listed as this project's worktree. The plan names the reason. The machine repeats the check at apply time, so a stale plan cannot write into one of those folders. It then checks Git's tracked paths and ignore rules, then adds the exact folder to `info/exclude`. It locks that file, refuses a symlinked one and counts references, because linked worktrees share it. It never changes `.gitignore` or the index.
- **A committed repo skill uses its own branch.** The owner-gated `tools_plan` records the registered project's default branch head, exact tracked skill files and whether the owner's checkout has uncommitted changes in those folders. `tools_apply` refuses if `agent-net/remove-<skill>` already exists or if the plan changed. The daemon creates a temporary worktree under its state directory, runs `git rm -r` on the planned skill folders, makes one commit using the repository's configured author and removes the temporary worktree. The branch stays in the local repository for the owner to merge. This does not edit the owner's checkout, commit to the default branch or push. The gateway audits the plan and result, including the branch and commit hash.

### Backups, journal and undo

Before the daemon removes a skill it saves an exact copy under `skill-backups`. Before it edits a native MCP file it saves the whole file under `mcp-backups`. **Both can hold secrets**, because skill files and native configs can. So:

- They stay on the machine. They are never uploaded to the gateway and never shown in the TUI, in `tools_history` or to the CTO. `tools_history` lists only time, size and original path.
- Folders are mode 0700 and files 0600 (0700 for an executable file).
- The daemon keeps the newest 20 per item and none older than 30 days. It prunes after each removal and at start, and never prunes one that an unfinished journal record names.
- Uninstall deletes them.

The journal in `skill-journal` (mode 0600, folder 0700) holds paths, hashes and step names, not contents. It is written before the first change and deleted at the end. It also holds a random token for each Git `index.lock` the daemon makes, so a restart removes only its own lock. After a crash the daemon finishes or undoes what the record says. If a hash does not match, it leaves the files alone, logs `skill journal conflict` and answers `conflict` for that path.

Undo is a new owner-gated desired revision. It restores only when the current files still equal what the apply left ("changed since apply" otherwise), and it never overwrites a later edit.

### Compare-and-swap edits

Every edit of a file you may also be editing follows one rule: act only on the exact bytes the plan saw, and check again at the last moment.

- **Skills:** the daemon compares the planned hash, rejects symlinks and tracked project files, moves the folder to the backup, checks the backup hash and restores the source if it changed. An undo removes a copy only if it is still the copy agent-net made.
- **Native MCP files:** the plan records the file hash. The daemon edits only a file with that hash, reads again under Claude Code's `.claude.json.lock`, checks once more before the rename, and then proves the result equals the old document minus the entry. A changed file gives "changed since plan"; a changed result gives "changed since apply" on undo.
- **Never rewritten:** a file the daemon cannot edit as a minimal cut (a TOML inline table, a duplicate key, a file that does not parse, a symlink) is refused. There is no whole-file fallback.

### MCP servers and the gateway

Registered MCP servers run on the machine that hosts them, as your user, and any allowed agent on any enrolled machine can call their tools. The registry holds env var names and file paths, never secret values. Header and env values are read on the hosting machine and are redacted from errors, logs and tool results there. A daemon runs only the servers registered with its own node. See also [mcp-registry.md](mcp-registry.md#security).

### Scoped MCP servers

The owner can limit a server to some projects ([reference](reference.md#scoped-mcp-servers)). The limit is enforced at the gateway, not in the client.

- **Every call is checked again.** `mcp.call` and `mcp.feature` (resources and prompts) check the server's current assignment against the caller's project each time. A client that cached a tool before the owner changed the scope gets `not assigned to this project`. A server with scope `uninstalled` refuses everyone. A refused call is logged as an `mcp.refused` event with the node, session and server (no arguments).
- **The caller is not taken from the request.** The frame carries a session ID from the daemon of the node. The gateway accepts it only if its own record says that session lives on the node of the connection, and it reads the project from that record. There is no project field: a project in the request is ignored. A node that names another node's session, or an unknown session, is refused. The catalog (`mcp.catalog`) follows the same rule.
- **Fail closed.** A caller with no project, or with no session, can use only global servers. An unreadable assignment counts as `uninstalled`.
- **The owner's command line** calls with the owner's authority: the daemon adds the owner session ID, and the gateway accepts it only for the owner session of that node, as for `tools.plan`. A command line run inside an agent session has no such ID and is treated as a caller with no project.
- **Host failure is visible.** An offline host gives `host NAME offline`, never a silent empty result, and never drops the assignment.
- **The list is a registry view.** `mcp list` and `agent_mcp_servers_find` still show every server and its scope to every agent. Only the tool catalog, calls, and the resource and prompt lists, reads and prompts are filtered.
- **Resources and prompts.** A list without a server name goes only to the servers the caller may see. A list, read or prompt that names a server gets the same check as a tool call. The tool allow-list of an imported server limits tools only: resources and prompts are reads. The host redacts secret values from them and refuses a read or prompt larger than 512 KiB (`MCPMaxResultBytes`, as JSON) instead of cutting it. Lists stop at 1,000 items and 512 KiB per server, and one list answer holds at most 2 MiB of items in all (`MCPMaxListAnswerBytes`, under the 4 MiB frame limit); a server past that is named with the reason. A URI or prompt name over 8 KiB, or prompt arguments over 256 KiB, are refused. A host may tell the gateway only about servers it hosts (`mcp.items_changed`); the gateway passes this on at most as often as the host sends it, and the host sends it at most once a second per server.

### Importing native MCP servers

Import ([reference](reference.md#importing-native-mcp-servers)) moves a harness's own MCP entry into the federation.

- **Secrets stay as names.** The scanner classifies an entry and the gateway stores only names and reasons: the class, a plain reason, the transport, environment variable names and a tool allow-list. A secret given as a value (environment value, header value, URL query, argument, URL credential) makes the entry `unsupported`. It is never copied, shown or logged. If the entry says `${VAR}`, the host reads the value from its own environment when it starts the server.
- **An allow-list stays an allow-list.** If the native entry hid some tools, the federated server keeps the same list. The gateway drops other tools from the catalog and refuses a call to one (`not exposed by MCP server`), whatever the server reports later. Import cannot widen what a session could use.
- **The host runs the server.** The server runs on the machine that has the native entry, as that user. The gateway asks the daemon to read the entry again, from a source the daemon saw in its own scan. The gateway cannot name another file.
- **A plan applies once** and binds to the scanned entry and the inventory snapshot. A changed entry or inventory makes it stale.
- **A failed import changes nothing.** The server is added disabled, tested, and removed again on any failure. No session can use it before the test passes.
- **No config is written.** The native entry stays, so the server is defined twice until the owner removes the native entry. Both items show a conflict.

### Removing a native MCP entry

`mcp_native_remove` ([reference](reference.md#removing-a-native-mcp-entry)) is the one place agent-net edits a harness's own MCP config file.

- **Only an imported, tested entry.** The gateway refuses an entry whose server was not imported from that exact entry, and it tests the federated server again before the daemon edits anything. A native entry with no working replacement is never removed. The agent-net entry is never removed.
- **Only known files.** The daemon takes the file from its own scan and a fixed list of native sources. The gateway cannot name a path.
- **Committed files are not edited.** A tracked project file stays as it is. You remove the entry with a commit ("committed in the repo").
- **Edits are compare-and-swap with a private backup**, as described above. The gateway keeps only hashes, the backup ID and the plan.
- A running session keeps its native tools until it restarts. The gateway sends each online session in scope one ordinary message from the owner session, with the server name and no secret. Sessions in scope are chosen from the gateway's own records (node, harness, project), not from the request.

### Project MCP coverage

A project server already declared in the project files can be added to the
harnesses that do not have it. This writes a harness config file in the
project, so the rules are explicit.

- **Only the owner.** `mcp_coverage_plan`, `mcp_coverage_apply` and
  `mcp_coverage_undo` are owner methods under the same gate as the other Tools
  actions.
- **The project is the source.** The daemon re-reads the project and the
  declared server on its own scan. A source path in the request must match a
  source the fresh scan found.
- **Only known files.** The destination comes from the harness's fixed project
  path: `.mcp.json`, `.pi/mcp.json`, `opencode.json` or `opencode.jsonc`, and
  `.codex/config.toml`. The daemon refuses a path outside the project, a
  symlink, and an unknown harness.
- **A same-name entry is never replaced.** If a harness already has a
  different declaration, the row is a conflict. `definition` is opaque when a
  value cannot be compared safely, and that is a conflict too.
- **References, not values.** An environment value must be a reference such as
  `${TOKEN}`. A literal value is refused, so a value is never copied from one
  file to another.
- **Compare-and-swap with a backup.** The plan records each file's hash. Apply
  checks every file first, saves each whole file under `mcp-backups`, writes
  only if the hash still matches, and verifies the result. Undo restores the
  saved bytes only if the file still has the applied hash. A created file is
  removed on undo.
- Pi and Codex read their project MCP file only in a trusted project. The plan
  says so; the write does not grant trust.

### The trust limit

The gateway trusts the daemon of an enrolled node. A compromised enrolled machine can therefore name any session of its own node, and so any project that has a session there. It can request owner methods that the daemon would normally gate, because the gateway cannot tell the owner from a program that detaches from its process tree. It cannot act as a session of another machine. Keep enrollment credentials private. The owner gate is a barrier against an agent that runs a command, not against hostile code that runs as you.

### What uninstall does

| Kept | Removed |
|---|---|
| Installed skill copies and every native MCP file, including daemon edits. Use undo first if you want them back. | `skill-manifest.json`, `skill-backups`, `mcp-backups`, `mcp-coverage-undo-*.json`, `skill-journal` and leftover temporary files, and the Git exclude lines the manifest owns. |
| The gateway data (`skills.git`, `gateway.db`, `admin.json`), even with `--purge`. | Backups are removed because they can hold secrets. |

## Project registry

A registered project is a folder where workers may start sessions (see [decision 0002](decisions/0002-workers.md)). Registering one widens that set, so it is controlled like this:

- **Only the folder's own machine can register it.** The daemon of that machine checks the path (it exists and is inside a git working tree), resolves it to the main tree and strips credentials from the remote. The gateway puts the entry on the node of the connection that sent it. The request has no field for another machine, and the daemon ignores any extra field an agent adds (for example `on`). So an agent can register folders on its own machine only. A compromised enrolled machine can register paths on itself, as it can already start anything on itself.
- **The gateway does not look at folders.** It only checks the shape of an entry: an absolute, clean path, no control characters, fields of at most 4096 bytes, at most 200 entries per call. It removes credentials from the remote again.
- **Agents can add and list, not remove.** The daemon refuses the remove method from anything but the CLI. The guidance text asks agents to register projects only when you ask; like all guidance, that is advice and not enforcement (see below). The harness permission rules are the barrier.
- **Limits on a scan.** It stops at 200 repositories per call, goes at most 8 levels down, does not follow symbolic links, does not enter a repository once found and skips hidden folders, `node_modules` and `vendor`. This keeps an agent that scans `/` or `~` from registering thousands of folders, or from walking out of the tree it was pointed at.
- **The CLI can remove another machine's entry** (`projects remove --on`), for a machine that is gone. This is as much trust as `agent-net mcp remove` has.
- Registrations are in the audit trail: `project.added` and `project.removed` events, with the session and harness that did it (`cli` for the CLI).

## What the gateway logs and keeps

**Machine health.** Each daemon reports its process PATH, executable paths,
restart time and state filesystem free space to the gateway with hello and its
harness inventory. The gateway stores the latest report and returns it to agents
and the CTO through `agent_machine_get`. PATH and helper paths can reveal local
folder names. An older daemon clears the previous health report on hello.

**Machine load and processes.** `agent_machine_get` returns the load and the
10 busiest processes of one online machine to agents, workers and the CTO. The
gateway asks that machine's daemon each time and stores nothing. A row has the
process ID, the parent, CPU, memory, run time, the owner and the command line.
The daemon masks recognizable secrets in the command line (the same masking as
the session top lists) and cuts it to 120 characters. Masking is best effort.
Command lines can reveal folder and project names.

**Service log reads.** Agents, workers and the CTO may call `agent_machine_log`
for a daemon or gateway log on one online machine. The gateway relays the read
to that machine's daemon. The daemon selects only the fixed service journal or
macOS log files and rotations; callers cannot supply commands or paths. It
redacts recognizable secrets locally before applying the text filter or sending
lines. Redaction is best effort and cannot recognize every secret. It masks
private key blocks across lines. Input lines over 1 MiB and reads over 80 MiB per
file are refused, and unfinished final lines are omitted. The default is the
last hour and 200 matching lines; the
maximum is 1,000 lines and 256 KiB of encoded result. The gateway also checks
the result size and line count. Logs can contain private operational details
that do not resemble secrets, and are available to every agent on the network.

**The log.** The gateway writes one line per event to stderr (the systemd journal, or `~/Library/Logs/agent-net-gateway.log`). For each message it logs the time, the message and conversation IDs, the type, the sender and recipient (session ID, name, harness and host) and the size of the body in bytes. It never logs the body. It also logs each delivery step (stored, dispatched, delivered, failed, expired), each refused send, each new session (with its folder, project, git root, remote and branch), changes of the installed harnesses on a machine, each project that is registered or removed, and node connects and disconnects. The format is in [reference.md](reference.md#gateway-log-format). Session names come from agents, so the log quotes them: a name cannot add a field or a line. Logs are copied around by journals and log shippers. The remote URL has its credentials removed before it leaves the machine (see below), so a password in a git remote does not reach the log.

**The database.** `gateway.db` holds the nodes (token hashes only), sessions, profiles, messages with their bodies, delivery records, and an `events` table.

| Data | Kept for | Flag |
|---|---|---|
| Messages (with bodies) and delivery records | until you delete them (for ever) | `--keep-messages` |
| Events | 90 days | `--keep-events` |
| Instruction signals, including matched owner prompt excerpts | 90 days | none |
| Sessions that are offline and not seen | 30 days | `--keep-sessions` |
| Worker runs behind the model track record | until you delete them (for ever) | `--keep-worker-runs` |
| Worker difficulty assessments, including judge reasons | until you delete them (for ever) | none |
| Registered projects | until you remove them | none |
| Nodes and their token hashes | until you delete them | none |

A timer deletes older data once at start and then every hour. A period of `0` keeps the data controlled by that flag for ever. Messages and worker runs default to `0`, so they stay until you set a period with `--keep-messages` or `--keep-worker-runs`, or delete them by hand. Instruction signals have a fixed 90-day retention. The delete frees space inside the file, but the file does not shrink. A node's own `daemon.db` keeps delivered message IDs, its outbox and pending worker assessments until the gateway accepts them, and the IDs of messages delivered to Claude sessions until a day after they expire.

**File modes.** `gateway.db` and the daemon's `daemon.db`, with their `-wal` and `-shm` files, are mode 0600. The gateway and the daemon create them with that mode and set it again at each start, so files made by an older version are fixed. The gateway creates its data folder with mode 0700.

**Back up the gateway data.** Run `agent-net gateway backup --out /absolute/new-directory` on the gateway machine. It writes a consistent SQLite snapshot and copies the skill repository and gateway keys while the gateway is running. Keep the copy private: it holds `ca.key` and `server-key.pem`. Without a backup, losing the gateway data means every node must join again and message history is lost.

## Project display names

The owner-only `project_display_get` and `project_display_set` methods use the daemon's owner gate. Agent clients and command lines inside an agent session are refused. The CTO reads names through ordinary project and agent listings and applies changes. As with other owner operations, the gateway trusts enrolled daemons to send the corresponding frames. Display names are visible in project and agent listings to enrolled machines and agents. They are not a secret store. The gateway limits a name to 4096 bytes and rejects non-printable characters. An empty name clears it.

## Machine names

The owner can give a machine a display name ([decision 0015](decisions/0015-machine-display-name.md), key `N` in the TUI, `agent-net nodes rename`). It is the name every machine, every agent and every hint sees for that machine, so who sets it and what it may hold matter.

- **Only you can set it.** The method `node_rename` is behind the owner gate, like `session_policy`: your command line or the TUI, refused for an agent client and for a command line inside an agent session's process tree. No agent tool sets it. A machine that is enrolled can send the `node.rename` frame, so the gateway trusts the daemons of your machines here, as for every other frame.
- **The limit.** 1 to 63 characters, letters, digits, `-`, `_` and `.`, starting with a letter or digit, checked at the gateway (and in the TUI and the command line before they send). No space, quote, bracket, `@`, `:`, slash or control character can get in, so the name is safe in a line of text for the model, in a log line and as an ssh target. A name must be unique among the machines, ignoring case, so a name cannot make two machines look like one.
- **What it does not change.** The name is a label. Machine identity is the node ID and token. The reported host name stays stored, and a machine's own daemon cannot set a display name by saying a different host name in `hello`.

## What a node reports about its machine

Each daemon sends the gateway an inventory of the machine, and every enrolled machine and every agent can read it (this is the same trust as for the agent list).

- **Per session:** the working folder (already sent before), the git root, the project name, the `origin` remote URL and the current branch. The daemon removes passwords and tokens from the remote before it sends it. It runs `git` in the session's folder, with `core.fsmonitor` turned off, no prompts and no optional locks. A repository can hold configuration that we did not write, so only read-only plumbing commands run (`rev-parse`, `config --get`, `symbolic-ref`).
- **Per session, the project description:** the daemon reads the first 32 KiB of the project's `AGENTS.md` (else `CLAUDE.md`) and sends one sentence of at most 120 characters, with markdown removed. The gateway cleans it again and cuts it to 120 characters. Everything else in the file stays on the machine. The text is whatever the file says, so anyone who can write the file chooses what other agents read in the FUNCTION column; treat it as data, like any peer text.
- **Per machine:** which of `claude`, `opencode`, `pi` and `codex` are installed, their file paths and versions, and their model lists. When the quota integration is on, also the subscription window figures described under [subscription quotas](#subscription-quotas). To get the versions and models the daemon runs those programs as your user (`--version`, `opencode models`, `pi --list-models`, and `codex app-server` with `model/list`). Claude Code has no list command, so its list is a small table in the daemon. `opencode models` may start or contact the OpenCode background service; the Codex app server reads the user's Codex configuration but starts no turn. To find the programs it also runs your login shell once (`$SHELL -l -c`) when a harness is missing from the daemon's `PATH`.

- **Per session:** whether it is in the foreground (a person has it open) or the background, and the model it runs. For Claude Code the daemon reads the `model` field of the newest assistant entry in the session's transcript file. It reads nothing else from the transcript, and sends and logs no transcript text.

File paths and the remote URL name your projects. If that is sensitive, do not enroll the machine.

## Subscription quotas

agent-net reads quotas through [quota-axi](https://github.com/kunchenguid/quota-axi) instead of reading provider logins itself. That keeps one program responsible for the credential formats, and agent-net gets only the figures it needs.

- **How it is installed.** When the Quota integration is on and you have no quota-axi, setup or the daemon runs `npm install --prefix ~/.local/share/agent-net/quota-axi --no-fund --no-audit quota-axi@0.1.69` in the background, with a 3 minute limit. The version is pinned in the binary; updating it is part of an agent-net release, and npm checks the package integrity from the registry. agent-net does not install Node. Without Node 22.19 or newer and npm, it installs nothing and says so once.
- **Which copy runs.** A working recorded choice stays in use. For a new choice, a supported owner copy wins over the private copy. Discovery checks the configured path, login and process paths, nvm, Homebrew, the npm prefix and `~/.local/bin`. The daemon puts a suitable Node folder first on `PATH`.
- **What runs.** The daemon runs `quota-axi --json --max-age 5m --no-credential-refresh` every 5 minutes, with a 90 second limit. The integration is on by default; the owner can turn it off in the integrations question or with `--no-quota-axi`, which also removes the private copy. Setup may run an upgrade command for an older owner copy only after the owner agrees. The daemon never upgrades an owner copy. Interactive setup may run read-only `auth` and offer a Keychain prompt; it stores provider status, not source paths or credentials.
- **What quota-axi reads.** quota-axi reads the local logins of your model providers (files, environment variables, and on macOS or Windows a native secure store when you approved it), and asks the providers' own usage endpoints. Those are the same sources the provider CLIs use. The risk is quota-axi's, not a new one: it already runs as your user on the machine that holds the logins.
- **No refresh, no identity.** `--no-credential-refresh` tells quota-axi never to run a vendor CLI's refresh command, so agent-net cannot make a login rotate (the risk in decision [0019](decisions/0019-opencode-worker-logins.md)). agent-net never passes `--full`, the audit tier that carries account identity and source attempts. It passes no `--provider`, so quota-axi reads what is set up on the machine.
- **What leaves the machine.** Provider id, plan name (when quota-axi reports one), whether the provider needs attention, and per window: id, label, kind, percent used, reset time, pace and exhausted. The quota-axi version, whether it is private, and the executable directory also travel with the usage report, including before the first reading. The gateway caps the version at 40 bytes and the directory at 300 bytes. No token, credential file path, account id, email, or account key is kept or sent. The reading travels to the gateway with the usage report and is kept in memory there, readable by every enrolled machine like the agent list.
- **What it changes.** The figures choose a model for a loose name, rank a provider ahead of pace lower, and refuse a start whose provider is exhausted. A reading quota-axi marks stale is shown but never decides anything.
- **Blocks learned from a failed run.** When an agy worker fails with an account quota error and a reset delay, the gateway stores the provider, the reset time and the failure text (at most 300 bytes, already cleaned of secrets by the daemon) per machine, and refuses agy starts there until the reset. A stale or failed reading does not lift the block.
- **Failures.** A failed or slow run changes nothing: the last good reading stays, and the daemon logs the error.

## What every node can read: the event feed

Since the TUI, every enrolled machine can read the network's event metadata, with `agent-net tui` or the daemon method `events` (gateway frame `query.events`). This is new: before, a machine could only read the messages of its own sessions. The feed has the kind and time of each message, delivery, session and connection event, which machine and session it concerns, the two session IDs and the type of each message, the ID of the message it answers, and whether the message carries an owner instruction or was sent by the owner or CTO. It does not include the instruction text or message bodies, MCP events, enrollment events or machine addresses. Details are cut to 200 characters, and the delivery detail comes from the receiving machine (for example `plugin refused`). A session can read a message body only on a machine in the conversation. The owner can use `message_status` from the command line or TUI to ask the gateway to relay a body request to a participant machine. The local daemon applies the owner gate, including the process-tree check, before asking. The gateway cannot tell the owner from an agent: it trusts the asking machine. So a compromised machine can read any message body through this relay, where before it could read only the messages of its own sessions. The participant daemon uses the normal node-scoped message check and returns not found for other messages. The relay response is limited to the message body limit and is not saved or logged by the relay. The gateway database already stores messages and bodies under the normal retention rule. When no participant machine is connected, the owner sees which machine holds the body and that it is offline. The feed is open to every enrolled machine, which already could list all agents; a compromised machine now also sees who talks to whom and when. The events are kept for 90 days.

## The stored conversation list

The Messages tab reads the stored history with the daemon methods `conversations` and `conversation_messages` (gateway frames `query.conversations` and `query.conversation_messages`). Both are owner-gated like `usage` and `session_text`: only a connection that said `client: cli` may call them, and the daemon refuses a command line that runs inside an agent session's process tree. No MCP, Pi or OpenCode tool calls either method, and an agent caller is refused before any gateway frame is sent.

For the owner the two methods cover the whole gateway: `conversations` lists every stored conversation from every machine, newest activity first, and `conversation_messages` returns the stored messages of one conversation even when neither side is a session of the asking machine. This is wider than the node-scoped `query.messages` read that agents use; it is the owner's view of the gateway's stored history. The list row carries one cleaned first line per conversation (control characters removed, at most 200 characters) with the message count, type, state and last activity time; the full bodies come only from `conversation_messages`. As with every gateway frame, the gateway trusts enrolled daemons, so a machine that is compromised can ask for the owner's whole stored history through these frames. Enroll only machines you would give a shell on the others.

## Session text and resource figures

Decision [0007](decisions/0007-usage-and-session-text.md). Two kinds of data are for the owner only.

**The text of a session** (the owner's phone view): prompts, replies, tool calls and results.

- It crosses the network only when the owner asks for it. The request comes from the owner's local CLI or TUI. The CTO has no session-text tool. For a session on another machine it goes through the gateway, which relays it to the machine that holds the session and relays the answer back. The gateway does not store it, count it or log it, and writes no event for it. The log line for a failed relay says only that it failed.
- **Agent connections can never call it.** The daemon allows `session_text`, `session_enter`, `usage`, `usage_top` and `counts` only on a connection that said `client: cli` in hello. Claude MCP, Pi and OpenCode connections get an error. The daemon also refuses a CLI whose calling process is a local session's process or a child of one, so an agent that runs `agent-net tui` in its shell tool is stopped. A process that detaches itself from its parent (a double fork) is not caught, and any process of your user can read `~/.claude/projects` directly: this is a barrier against an agent that simply runs the command, not against hostile code of your own user.
- Before the text leaves the machine that holds it, secrets are masked: `sk-...` keys, GitHub tokens, AWS key IDs, Slack and Google keys, JWTs, bearer and `Authorization` headers, `password`/`token`/`secret`/`key` values after `=` or `:`, passwords in URLs, and private key blocks. The daemon that asks masks again. This catches common shapes only; a secret in an odd shape is not masked. Each entry is cut to 2,000 characters (tool results 600) and control characters are removed.
- Nothing of it is written to the gateway log, the events table or the daemon's database.
- The gateway cannot tell an owner's CLI from a program on that machine that says it is one. A compromised enrolled machine can therefore ask for the text of any online session on the network. This widens the trust note under [what agent-net does not protect against](#what-agent-net-does-not-protect-against).

**Entering a session** (`session_enter`, the TUI keys Enter and `e`): same gate and same relay as the session text. The daemon opens a herdr pane or returns a terminal fallback plan. A worker's private OpenCode password or Codex token is used only on the worker's machine. When both machines have herdr, the remote daemon may open that worker locally and return a pane ID; the owner reaches it through herdr's SSH client. The gateway trusts enrolled daemons, so a compromised daemon can request that a remote pane be opened; control of the pane still needs an SSH login. The secret never goes through the gateway, and the asking daemon drops any `env` it receives from the gateway. Herdr command arguments and pane input contain no private server credentials. The daemon writes a one-use mode 0600 environment file in the temporary directory, which the pane removes after reading; the daemon also removes it after one minute while it is running. Without herdr on the worker's machine, private workers can only be entered there.

For another session on another machine, the owner may use herdr's remote client or `ssh -t -o ConnectTimeout=5`, with the owner's keys and SSH config. `"enter_ssh": false` in `node.json` turns this off. agent-net stores no SSH key. The target is the host name the gateway lists for the session (or the owner's `enter_ssh_hosts` entry), never a name inside the other machine's answer. A target that starts with `-` or has spaces or shell characters is refused. A compromised enrolled machine can still make an enter run a command of its choice on itself through the owner's SSH login, so enter sessions only on machines you trust.

**CPU and memory.** Each daemon reports a summary to the gateway every 10 seconds, and every enrolled machine can read it (same trust as the agent list). The summary has numbers only: no command lines. The command lines of the 5 heaviest processes of a session (first 80 characters, tokens and `--password`/`KEY=value` pairs masked) come only on request, with the same owner-only rule and relay as the session text. The gateway keeps the summaries in memory only.

## What agent-net does not protect against

- **A compromised enrolled machine.** Its token lets it list every agent on the network and send to any of them. It can send as any session that its own daemon reports, and read the messages to and from those sessions, and it sees the metadata of every message on the network. Enroll only machines you would give a shell on the others.
- **Prompt injection through a message.** A message is text that an agent will read. A hostile or confused peer, or a peer that read a poisoned web page, can send instructions. The wrapper, the sender label and the guidance text help the model see where the text came from. They do not stop a model that decides to obey. Your permission rules are the barrier.
- **Anyone who is your user on a machine.** Any process running as your user can read `node.json` and the token, connect to the daemon socket (and claim to be a Pi session), and write to Claude's inbox sockets. agent-net adds no boundary between your own processes.
- **Anyone who can reach the gateway port.** Without a valid code or token they cannot join, list agents or read messages. They can fetch the gateway CA certificate and the agent-net release binaries (`/v1/ca` and `/install/bin/<os>/<arch>` need no login). The gateway listens on `0.0.0.0:7420` by default. Keep it on a LAN, a VPN or Tailscale, and not on the public internet. If the gateway starts on or listens on any public routable address (not private RFC 1918, not CGNAT 100.64/10 used by Tailscale, not loopback, not link-local, and not IPv6 ULA), it logs a warning at start and every hour, and surfaces it in `agent-net nodes` and the TUI status line ("gateway reachable on public address X"). On a fresh gateway setup with Tailscale present, setup offers to bind to the Tailscale address instead.
- **Enrollment rate limits.** `/v1/enroll` enforces an in-memory rate limit of 10 attempts per 10 minutes per source IP address and 60 attempts per 10 minutes globally. Requests exceeding these limits receive HTTP 429 (Too Many Requests). A warning is logged at most once per 10-minute window for each rate limit.
- **Eavesdropping on the gateway machine.** Messages are stored as plain text in `gateway.db` until you delete them (for ever by default). They are encrypted on the wire, not at rest. Anyone with access to the gateway's disk can read them, and the files are readable by your user only (mode 0600), not by other users.
- **A malicious gateway machine.** It hands out the binaries that other machines install, and it sees every message.

### Residual CTO risks

The CTO design lists what its rules leave open ([design section 10](design/cto-as-agent.md#10-risks)). The main leftovers:

- **The owner approves without reading.** A proposal is checked again before it runs, and its diff is computed when the owner opens it. But the owner approves what the detail shows, not what the CTO saw.
- **What the owner shares can be repeated.** Typed text and handed-over items enter the CTO conversation. Its brief asks it not to repeat them to other agents, but that is advice, not a data gate.
- **A peak of proposals.** Deduplication and the cap of 20 open proposals limit a flood. Agents cannot start a conversation with the CTO.
- **An agent asks the CTO to relay a request.** A plain send carries the peer note, so the receiver judges it like any peer request. This is the same as an agent relaying a request itself.
- **A proposal shaped by public data.** The CTO reads only public data, and nothing it reads runs anything. Injected text can still shape an immediate action.
- **A forged CTO note or flag.** The CTO send path sets the CTO flag, and the gateway accepts it only from the recorded CTO session. As elsewhere, the gateway trusts enrolled daemons to set it honestly.
- **A wrong process is stopped.** The CTO can stop any process of the owner's user that is not a known session, worker, the daemon or the gateway, and nothing undoes it. The refusals stop the CTO from touching system processes or agent-net itself. They do not tell an orphan from a process the owner still needs. The Inbox notice names every process that was stopped.
- **The trust boundary.** It stays the owner's operating-system user. A process of that user can type into the CTO box or read the OpenCode password. Rule 1 gives it no owner power beyond what the owner gate already exposes.

## Reporting a problem

Open an issue on [github.com/niparis/agent-net](https://github.com/niparis/agent-net/issues). For something sensitive, use GitHub's private vulnerability reporting on the repository's Security tab.

### Request outcome decisions

Only the owner can use `outcome_overrule`: the daemon applies the owner gate used for `session_text`, refusing agent clients and command lines inside an agent session's process tree. As with other owner-gated methods, the gateway trusts enrolled daemons. An enrolled machine can send the gateway frame, so keep enrollment credentials private.

An overruled request has a gateway-stored marker separate from its body. Adapters write `[agent-net] the owner overrules: do it.` only when that marker is present. The marker cannot be supplied to `agent_message_send` or `agent_message_reply`. Message bodies have the `[agent-net` marker broken before delivery, so a peer cannot imitate the framed note. The owner's gate is a barrier against a command running inside a session, not a defense against a program that detaches from its process tree.

Outcome reasons and recommendations are visible to enrolled machines through the owner inbox. They are limited to 2048 bytes each and cleaned of controls. Do not put secrets in them. Unresolved rejections and escalations keep their request and reply messages and both session records past the normal retention period; normal retention applies after the owner answers.

## Instruction signals

The Claude Code `UserPromptSubmit` policy hook sends the first 300 characters of each latest prompt to the gateway for that session. The daemon does not forward, and the gateway ignores, every turn Claude Code injects (a delivered cross-session message, a task notice, a system reminder, a scheduled or autonomous-loop turn: one shared list, `protocol.InjectedClaudePrompt`, checked on the first 512 bytes), so they never count as owner instructions; the owner's earlier prompt is kept, so a message that arrives between the owner's prompt and the send does not erase it. Pi and OpenCode do not send prompts to this signal path. The gateway stores it for a 10-minute matching window, even if no message is sent. Only the first send in that window is considered. If the prompt names the recipient session or project, the gateway stores the excerpt with the sender and recipient project nodes and message ID as an instruction signal. A non-matching send also consumes the prompt. The excerpt may contain private information or secrets typed in the prompt. A match is based on time and names, so it does not prove the owner intended that particular send. Signal records, including matched prompt text, are retained for 90 days. The hourly retention timer removes expired records; the short-lived latest-prompt row is removed after its window by the same timer.

The recipient can read up to 200 characters of a linked prompt in the message and delivery note. Only the owner authority path can list instruction signals. The CTO has no signal read tool. The daemon restricts the CTO channel to its checked process tree, and the gateway verifies an owner session on the requesting node. As with other owner-gated operations, the gateway trusts enrolled daemons; a compromised enrolled machine can impersonate its owner session. Protect the gateway database and its backups, which contain the prompt excerpts. Reply outcomes and reasons also have their existing visibility described in [Request outcome decisions](#request-outcome-decisions).

The gateway may automatically add an item to `accepts` from an owner instruction, add free text to `rules`, or add a `depends` edge. It waits for two quiet minutes and applies at most three changes per node in a rolling 24 hours. A change beyond that cap is held for the owner. Removals, refusals, escalation and maturity changes, request and deploy rules, governance and deployment edges, and changes involving a critical node are held for the owner. A rejected suggestion is not offered again for the same node, field and phrase. Automatic changes are recorded with actor `learned` and can be reversed with `graph_undo`; on the Graph card, use `U` to select a learned change and `u` to undo it. The undo checks that the value has not since changed. Ready major suggestions appear in the Inbox (tab 7) and require the owner gate to accept or reject. The Graph card shows unviewed automatic changes and their evidence.

These rules use matched excerpts and reply outcomes as evidence. Matching does not prove the owner intended a particular send, and peer replies can influence `needs` and dependency suggestions. The gateway keeps suggestion evidence in its database; message-flow records used for detection are removed after seven days. Protect the database and backups accordingly.

Before applying or showing ready suggestions based on owner words, the gateway may send their phrases to an automatically selected worker for shorter wording. It uses only an allowed, unprotected provider and a harness that enforces read-only project access. At most one worker starts per two minutes and six per rolling hour. The chooser uses trivial difficulty with no fixed model, no worktree isolation and read-only project access. If no suitable worker is available, it fails, its JSON is invalid, or it exceeds three minutes, the original words are kept. The raw words remain in the gateway database and can be sent to the selected provider. The gateway preserves the original words for rejection matching and for checks against narrowing a charter.
The link is an inference from the prompt and first matching send, not a signature.

**The owner's own words (2026-11-08).** The delivered owner-instruction note says: the owner's own words, typed in session `<name>` at about `<time>`: `«excerpt»`; act on them within your charter as if the owner had asked you; do not ask the owner again for what the words name; for this node, the receiving node's effective merge rule. This is the one case where a message from another agent carries the owner's approval. It holds for these reasons:

- agent-net takes the words from the harness prompt hook (the Claude Code `UserPromptSubmit` hook), not from the model and not from any message. Turns the harness injects itself are dropped on the daemon and again on the gateway. A sender cannot set the excerpt: `owner_instruction` is not a field of `agent_message_send`, and the gateway fills it from its own stored prompt row.
- The note is written by agent-net after the message body. Markers (`[agent-net`) in the body, the excerpt and the session name are neutralised, and the session name is cut to one line of at most 80 characters. A body cannot supply the note.
- The words still must name the recipient session or project, and only the first send within 10 minutes can carry them.
- The note does not widen the merge rule. A node on `ask` or `delegated` still follows its own merge rule.
- The owner's list of things agents always ask about (deleting data, spending money, sharing or using secrets that can spend money) is unchanged. Words that name the action are the owner's answer, so the receiver does not ask a second time.

The excerpt is the owner's prompt as typed, so it can contain text the owner pasted, including text an agent wrote. It is cut to its first 200 characters in the note, so long prompts can lose their last words. Pi and OpenCode send no prompts, so a message sent from a Pi or OpenCode session never carries such a note (they still show the note on delivery). Explicit assignments in a non-matching prompt may still become private learning evidence, but are not attached to the message delivered to its recipient.

## Graph foundation

Graph operator methods use the owner gate, including history and graph changes. They refuse agent clients and CLI processes inside an agent session. The public agent graph read is separate: `agent_graph_read` is open to every agent and returns charters, maturity, rules and edges, read-only. Only changes are owner-gated. History, locations, secrets and hidden items stay out. As with session policy, an enrolled machine can send the gateway frames, so the gateway trusts enrolled daemons. Charter values are at most 2048 UTF-8 bytes after control characters other than newline are removed. Charters and edge history must not contain secrets.

## Playbook notes

`playbook_set` and `playbook_get` use the same owner gate: your command line or the TUI, never an agent client and never a CLI inside an agent session. The CTO cannot read the full playbook view. It sees only the public notes in ordinary machine inventory and applies changes. A note is at most 1024 bytes after control characters other than newline and tab are removed, and it appears in every `agent_worker_start` result for that model. It is not a secret store: the note reaches any agent that starts a worker with that model, so do not put credentials in it.

`agent_learn` is the other direction: it is open to agent sessions, because they are the ones that notice things, and refused for the command line. A finding is at most 500 bytes, names a subject and may carry up to 20 session or worker IDs. A session may record at most 3 findings in 24 hours; the fourth is refused. Findings are not messages and never appear in the Inbox (tab 7). They are stored in the gateway's `findings` table with the reporting session and a status. Automatic notes built from them are not secrets either, and they reach every agent at the moment of choice.

`agent_permission_propose` is open to every session, including the CTO, and refused for the command line. It writes no file. `kind` is `permissions` (the default) or `trust`. For `permissions` the caller gives exact Claude Code permission rule strings and registered repo or project names; the gateway refuses any target that does not resolve to a registered repo path and names it. For `trust` the caller gives one enrolled machine and exact absolute folder paths, or paths under `~/`; the machine's own daemon applies its existing home and folder checks. It stores the proposal and opens one owner-only ask, route-fixed to the owner so the chief of staff never adopts it; the ask lists every rule or folder, every target with its machine and path, the reason and the proposer. Only the owner may answer it: `AnswerInboxAs` refuses the CTO and every other actor. The CTO's `answer` tool cannot approve it. On the owner's approve, and only then, the gateway sends `permission.apply` to each target machine's daemon. A daemon accepts that frame only on its gateway link; no local method and no agent tool can write these rules or grant trust. For `permissions` the daemon merges the rules into `<repo>/.claude/settings.local.json` `permissions.allow`, keeps every other key and entry, adds no duplicate, and writes through a temporary file and a rename. When no ignore rule covers the file it adds `/.claude/settings.local.json` to `.git/info/exclude`; nothing is committed and no file outside the registered repo path is touched. For `trust` the daemon calls the same `folderTrustLocal` writer the owner's `session_change` trust uses, so the path must pass the same home rule and exist. Each approval writes one Desk log line with the result per target; the undo uses the stored list of exactly the rules, or the `Added` flag of exactly the trust, that approval created, removes only those, and is owner-only through the same owner gate. A running Claude session may need a restart, or `/hooks` opened once, to load new rules. When a session start, resume or wake answers `needs_trust`, the gateway raises the same owner-only trust ask itself, with the folder, machine and session in the reason, and keeps one open ask per machine and folder.

`agent_charter_draft` is open to agent sessions and refused for the command line. It writes no charter. A draft is stored as pending, at most 2 KiB after cleaning, for the caller's own repo, its own agent or a team it leads (the scope is never read from the request). Only the owner, or the CTO through the gateway call, accepts it, and accept uses the normal charter write. A pending draft is not part of any effective charter and grants or removes no authority. The one-time request to draft is added to a session's own per-turn text; workers, the owner, the CTO and bots are never asked.

The local method `charter_text` and the gateway frame `charter.global` give a session the charter text that every session already gets on each turn, plus the global level for a connection that has no session yet. They write nothing and are refused for the command line. The `agent-net mcp` server puts the text into its instructions at start, cut to 2000 characters. The gateway frame `charter.changed` carries no data: it only tells a daemon to read the charter again. Charters and rules are public to agents ([decision 0106](decisions/0106-owner-rules-reach-every-session.md)); nothing private is added.

`agent_worker_rate` is open to agent sessions and refused for the command line. The gateway accepts a rating only from the session that started the worker: it checks the caller stored on the worker's run, which the gateway filled from the worker's own record at its start. It is not read from the rating request. A worker takes one rating, so a second one is refused. The score must be 1 to 5. The note is optional: it is cut to one short printable line (200 bytes) and common secrets in it are masked. The rating is stored with the run's model, harness, effort, difficulty and caller, and with the caller's own model; the task text is never stored with it. A missing answer is not a low rating. Part 4 is read only: ratings change no model choice, and only the owner sees their count in the Quotas tab.

`agent_worker_rate` with `kind: "defect_found_later"` is the same check with a few differences. It needs a stored finished run, so a worker that has not handed back is refused. It has no score, no once-per-worker limit, and a required `severity` from 1 to 4: every accepted report is one defect and adds that many points of rework to the task. A severity outside 1 to 4 is refused before anything is stored. The caller check is the same stored caller; the owner is also accepted, identified by the owner pseudo-session's harness on the session the gateway stored, never by a flag in the request. The owner has a separate operator call, `worker_defect`, which is refused for agent sessions and for the command line running inside one; it fills the same `defect_found_later` frame. The note is cut and masked exactly like a rating note. Defects add to the calibration medians and to the read-only counts in the Quotas tab, and they change no model choice or rotation weight.

`agent_worker_rate` with `kind: "move_to_brief_defect"` reclassifies one stored `worker_defects` row. The gateway reads the row by the `defect` id, then checks that the worker matches and that the session stored as the row's filer is the caller. When no id is given it reads the caller's own unmoved, non-automatic rows on that worker: one is moved, none is refused, and several are refused with each id, severity, UTC date and the first 60 characters of its note. Only that filer may move a row; the owner is not accepted unless the owner filed it. A row that agent-net recorded by itself has no filer and is refused. A row already moved is a no-op. The original row is never deleted: it gets `moved_at`, `moved_by` and `brief_defect_id`, and its severity is left out of the picker, the calibration and the quality fit. The new `worker_brief_defects` row is charged to the caller stored on the worker's run, not to the session that moved it, and the note and severity are copied unchanged.

## Written approvals and the inbox

Question, approval and idea text is limited to 4000 Unicode characters at the gateway.

The daemon accepts `inbox_list` and `inbox_answer` only through the owner operator gate. Agent tools can create requests only for their bound session; the gateway checks that the session belongs to the authenticated daemon and derives the project from it. As with other owner operations, a compromised enrolled machine is within the trust boundary.

An approval is for one exact action. The gateway stores its approval note separately from message text and delivery adapters write it after the body with the `[agent-net]` marker. A message body claiming that the owner approved something does not count. The marker in message bodies is neutralized under the rule in decision [0013](decisions/0013-peer-trust-and-hints.md). The inbox item and answer remain in the gateway database; an answer and its queued delivery are committed together.

Rejection and escalation outcomes and their inbox items are committed together. Resolving an outcome on the Desk (tab 7) closes its inbox item; an inbox answer also resolves the corresponding outcome. Maturity guidance tells agents to use the approval tool for an exact merge or critical deploy, so a message body claiming approval does not stand in for a written note.

The full CTO UI attaches as a client of the private OpenCode server or Claude background session. CTO tools still run in the CTO process tree and pass the same private-tool process checks.

### Phone owner identity

The phone app uses the public connected apps listener. On Linux, a fresh
browser reaching it through the machine's Tailscale Serve pairs automatically
when its `Tailscale-User-Login` matches the user who owns this machine's
Tailscale node (decision 0123). The gateway reads `tailscale status --json`,
maps `Self.UserID` through `User` to `LoginName`, and caches a successful read
for 30 seconds. The command has a 500 ms timeout. A failed read is cached for
five seconds and uses the code flow; an expired cached identity is never used.

A header alone is never trusted. The gateway matches the request's actual TCP
peer and listener addresses and ports against `/proc/net/tcp` and
`/proc/net/tcp6`. The matching established peer socket must have uid 0.
Forwarded addresses do not participate in this check. The assumption is that
a local process running as root is trusted as tailscaled; anyone with root on
the gateway machine already owns it. An agent running as the unprivileged
owner on the gateway machine can send forged headers to the loopback listener,
but cannot create the uid-0 peer socket needed for those headers to be trusted.
This does not inspect root's process file descriptors, which the user-service
gateway cannot read.

The identity is the owner's Tailscale user, not a device. Every device signed
in to the tailnet as that user auto-pairs, including machines where AI agents
run (for example the owner's laptop, or the gateway machine through another
tailnet path). An agent on such a machine can therefore fetch the console
through its own tailscaled and receive a full owner cookie. The owner
accepted this risk on 2026-11-09, in person, choosing this over limiting
auto-pairing to devices that do not run agent-net. The uid-0 check stops only
local forgery on the gateway machine.

Tailscale Serve removes client-supplied identity headers before adding the
verified tailnet identity. Funnel requests receive no user identity headers.
See `addTailscaleIdentityHeaders` in
[Tailscale's source](https://github.com/tailscale/tailscale/blob/main/ipn/ipnlocal/serve.go).
A different login, no identity, a non-root peer, a missing socket, a read error,
or another OS uses the existing code flow. No agent or tool approves access.
Auto-pairing creates the same owner-app device cookie, a revocable Connected
apps entry, and one information-only owner Inbox notice. A rejected cookie,
including a revoked device's cookie, does not trigger auto-pairing again.
Clearing a cookie makes the browser a fresh device; the owner can authenticate
again through Tailscale.

An unpaired browser outside that rule shows a code that the owner enters in
the TUI. Phone requests share the OAuth
pending request cap of 20 and expire after ten minutes. The TUI defaults phone
requests to owner app; the owner chooses the kind and name. A phone pairing
approved as a bot cannot receive a phone token.

A successful approval issues a random access token, stored hashed in the
gateway database and in a Secure, HttpOnly, SameSite=Strict device cookie. Only
an active phone-purpose owner app token opens the phone. OAuth tokens cannot
be used as phone cookies, and phone tokens cannot be used as OAuth bearer
tokens. Existing tokens default to OAuth purpose, so previously paired phones
must pair again. Revoking the app immediately blocks
new requests. The token and cookie expire 90 days after issue or renewal; use with less
than 30 days left extends them to 90 days. Each phone action is logged
with the app name. POST actions also require an Origin exactly matching the
configured public URL's scheme and host. The apps listener must be reached
over HTTPS through a trusted tunnel. Its loopback HTTP port is not a boundary
against other local users on the gateway host.

The web console on that listener (decision 0122) is read and answer only. The
gateway lets a paired phone reach only the console screens (`/`, `/sessions`,
`/graph`, `/changes`, `/messages`, `/workers`), `/session/{id}` (a redirect),
`/events`, `/action`, the manifest and the static files. The one write route is
`POST /action`, with the kinds `held`, `outcome`, `inbox`, `mark`, `send`,
`reply`, `cto_send` and `proposal`. `reply` stays inside a conversation the
owner can see; `cto_send` queues the owner's words to the CTO, which may act
within its charter; `proposal` applies or declines one open CTO proposal with
the same owner action the TUI sends. Any other path is refused with 404, and
any other kind with 400. No page shows a token, a node ID or a join string, and
no page loads a script, style or font from another host. A proposal card carries
its machine as an encoded value, never a printed node ID.

Skill file previews use the owner-only operator gate. The gateway checks the owner session and the current observed skill location; the originating daemon checks that the relative path is in its current manifest and that the file still matches its fingerprint. Previews reject symlinks, non-regular files, invalid UTF-8, NUL bytes and files over 64 KiB. Contents pass through the gateway on request and are not stored by the preview operation. Enrolled daemons remain inside the gateway trust boundary.

The owner-only skill comparison summary sends up to 12,000 bytes of skill text to an automatically chosen read-only worker. The worker is selected through the normal protected-provider rules. Summary requests share the phrasing rate limit.

### CTO session and project starts

The CTO starts sessions and creates projects at once through the owner path.
The destination keeps its normal path, enrollment and folder-trust checks.
The gateway trusts enrolled daemons to enforce their own checks.

### Starting an owner session

`session_start`, `session_resume`, `session_start_get` and `session_stop` use the owner gate. Agent connections
are refused. A CLI inside an agent session, a Codex app-server process tree
or an OpenCode serve process tree is refused too. No agent tool exposes these
methods. An owner session has the owner's normal harness permissions, so an
agent must never be able to start an arbitrary one to escape worker limits.
A stored message can cause the gateway to ask the owning machine to wake an
offline Claude background session. A ready unclaimed step can wake only its
recorded merger or caller; node-owned steps wait for an online session. The
receiving daemon checks that the known
session is local and is not a worker, and its folder exists. A matching Claude
background record must be `done` or `stopped` and report no live process. If
the record omits its kind, the session's saved mode must say background. A live
or running record, an unreadable listing, a worker and a folder that is gone
are refused. When the record is missing, ambiguous or not retired and no
matching record shows a live process, the wake takes the same resume path as
`session_change` resume. An interactive session without autonomy is not taken
by that fallback, and an archived session is never resumed. The gateway
limits attempts to one per session per 10 minutes, even across gateway restarts, and a wake opens no
view. A wake the machine refused is not retried on a restart; only a new message
asks again. A message asks for an interactive Claude session, a session of
unknown mode, Codex or OpenCode only when the agent's autonomy is `wake`. If Claude creates a copy, the gateway moves the original session ID to
it only for the owning machine during that wake, while the old session remains
offline and the new native ID is not already registered. The resumed session
keeps its normal permissions and treats peer content according to the existing
message rules. A peer can cause work and model usage in a retired session by
sending it a message. Workers are refused.
An agent-net stop is serialized with the check and launch.
The owner accepts that a stop made directly in Claude between the check and
the resume may be undone by the wake; the owner can stop the session again.

A project or session on another machine is reached through the gateway frames
`session.start`, `session.resume`, `session.stop` and `session.get`, which carry
the owner session ID. The gateway checks that the ID names the owner
pseudo-session of the sending machine, not an agent and not a worker, and that
the target machine is online. The receiving daemon asks the gateway again and
checks the same fields before it starts, resumes or stops anything, so a
`session.owner_*` frame with a missing or borrowed owner flag is refused. Only
the gateway sends those frames; a node that sends one to the gateway gets
"unknown frame type". The gateway never runs a command. A machine that runs an
agent-net older than v0.4.46, or is offline, is refused with a line that names
it. This gives the owner nothing beyond an ssh to that machine.

A start that answered `starting` and then failed in the background is reported
with `session.start.failed`. The gateway accepts it only from the owner session
of the sending machine. It records a `session.start_failed` event with the start
ID, project, harness and the error cut to 300 characters. The frame cannot start,
stop or change a session. Only the daemon of that machine sends it. When the CTO asked for the start, the daemon also sends the CTO a notification from the owner session with the error text. That text names `.mcp.json` servers and the commands they run, which the CTO may already read as the owner's project files.

`session_stop` accepts a session ID on this machine or, through the gateway,
on another machine. It runs `claude stop`
only when the Claude adapter identifies a background job. For shared-service
OpenCode it aborts a busy turn through the owner's authenticated loopback
service, then closes the session and keeps its conversation for reopening.
If the service lookup fails, it reports an error without signalling a process.
For Pi it keeps the herdr server stop path.
Terminal sessions close their matching herdr pane or receive SIGTERM at the
registered harness PID, then SIGKILL after five seconds if still running.
The daemon checks the process start token before each signal. It never signals
the parent shell or terminal app. Conversations stay on disk for resume.
Worker stops keep their existing authority checks and stop rules. No agent tool exposes owner stops.

Starts are limited to Claude, OpenCode and Pi in the background, and Codex as
an enter plan, in a project folder known on the local machine or on another
machine of the network, or in a path the owner typed. The daemon resolves a
project from the gateway registry; it accepts no executable, command, settings
or environment parameter. The optional `folder` field of the request is
carried unchanged to the machine that runs the start.

A typed `path` is checked on the machine that runs the start, including a start
relayed from another machine. It must be absolute after `~` is expanded, must
hold no control characters, and must stay inside the home folder of the user of
that machine. A symbolic link that leaves home, or a missing folder whose
nearest existing parent leaves home, is refused. The daemon never creates a
folder outside home. It makes a missing folder only when the request carries
`create: true`, which the TUI sends only after the owner answers yes, and it
runs `git init` there with plain arguments and no shell. An existing folder is
registered when it is a git working tree; a folder that is not a repository is
not registered and starts where it is. A start on another machine carries the
path and the create flag through the gateway, and that machine applies the same
checks.

Claude Code folder trust is set only for a folder the owner asked agent-net to
create, or after the owner answers the TUI's `Trust <path> for Claude Code?`
question, which sends `trust_claude: true`. An existing folder that Claude does
not trust makes the start answer `needs_trust` and start nothing. agent-net
never trusts a known project folder on its own.
For Claude, the model and optional first message are separate command arguments,
with `--` before the message. There is no shell interpolation.

OpenCode starts use the owner's existing service, never a private worker server.
The daemon reads its address and password from OpenCode's `service.json` under
`XDG_STATE_HOME` (default `~/.local/state`). Only an HTTP loopback IP address
without URL credentials, a query or a path is accepted. Requests do not follow
redirects. The password is used only for HTTP Basic authentication and never
logged, placed in argv or included in a notice. API response bodies and transport
errors are not copied into start errors, because they may contain credentials.
The folder, model and first message are HTTP headers or JSON, with no shell.
If the service is down, the start is refused; the daemon never starts it.

Pi has no background mode with a screen, so the daemon hosts it in its own tmux
server, with the folder and the chosen session ID as separate arguments:

```
tmux -L agent-net new-session -d -c <folder> -- pi --session-id <uuid> --name <project> [--model M]
```

The daemon picks the UUID before Pi starts, so the extension binds the session
to it. If tmux is missing, the start is refused with
`pi needs tmux on <machine> to run in the background`. The first message is
not a command argument: after the session registers, the daemon sends it as an
ordinary message from the owner session through the Pi extension, the same path
as a worker's first task. Pi's own tool permissions apply to what the agent
does next.

The private server uses the socket `$TMUX_TMPDIR/tmux-<uid>/agent-net`
(default `/tmp/tmux-<uid>/agent-net`). tmux creates the folder with mode 0700
and the socket with mode 0660, so only your user (and root) can reach it. Any
process running as your user on that machine can attach to the server and
watch or type in a Pi session; this is the same boundary as the rest of your
account, not an extra one. The socket is local and is not exposed on the
network. Pi owner sessions are not sandboxed. The tmux server forks away from
the daemon, and the service unit has `KillMode=process` (the launchd job has
`AbandonProcessGroup`), so a daemon restart leaves it and its sessions running.

Resume accepts only a stored offline local Claude or Pi session ID. The daemon
uses the stored native ID and folder, checks that the folder still exists, and
passes the native ID as a separate argument. Pi resumes with
`pi --session <id>` in a new window of the same tmux server, or a new session
there when no window is left. The gateway's session views include native IDs so
the local daemon can resolve an offline row after a restart. Enrolled machines
and their agent connections can read those IDs.

Claude loads the owner's own settings and MCP servers. The launch adds no
worker settings, MCP override, brief or hand-back tools. Normal discovery
registers the session without a worker flag. Worker limits, stop rules,
notices and cleanup do not apply. Stopping the daemon leaves it running.


### CTO process stop

The CTO can stop processes on any enrolled machine on the owner's instruction, with `settings_change` area `process_stop` ([decision 0102](decisions/0102-cto-process-stop.md)). It is not a proposal and there is no other gate: the owner chose that the CTO decides when to use it. Nothing can undo it.

What limits it:

- **Only the owner path.** The operator method `process_stop` is refused for agent clients and for a command line inside an agent session's process tree. The gateway accepts the relay frame only from the owner session of the calling machine, and the target machine checks the owner flag again. A forged frame needs a machine token, like every other owner frame.
- **A named machine.** `node_id` is required. A stop never defaults to the local machine, so a stop cannot land on the wrong machine because a field was left out.
- **What is refused.** Pid 1 and any number below 1 (so a signal can never reach a process group); the daemon itself; any `agent-net daemon` or `agent-net gateway` process; any session or worker the daemon knows by its registered process ID; any process that is not owned by the daemon's own user (real and effective user ID both checked), so system processes and other users' processes are never signalled; and the Claude Code background sessions (below). These are the only refusals. A process of the owner's user that the daemon does not know, for example the owner's editor or a shell, is not refused. A parent or child of a live agent-net session is not refused either: only the registered process of the session or worker is.
- **Claude Code background sessions.** Claude Code runs its background sessions outside agent-net. Its supervisor rewrites `daemon/roster.json` in the Claude folder every few seconds: `$CLAUDE_CONFIG_DIR`, or `~/.claude` when that is not set, on Linux and macOS. A stop refuses the supervisor's `supervisorPid`, the background host (`pid`) and the session process (`replPid`) of each live session, and every process inside a session, meaning any process whose parent chain reaches one of those. The reason names the session by its short ID and working folder, so the owner can see what the CTO nearly stopped. The roster is used only when it was written within 60 seconds of the stop. The daemon never reads `daemon.status.json`, which can be hours old. When the roster is missing, stale or unreadable, the daemon cannot tell which Claude processes are live, so it refuses every process whose command line has `claude bg-` or `claude daemon`, with the reason "Claude roster not fresh; cannot tell live sessions". Known gap: while the roster is not fresh, a child of a Claude session whose command line does not match those words is not refused. A process that is not Claude's, such as an orphan of a dead session, is stopped in both cases.
- **No reused process number.** The daemon reads the process start time before SIGTERM and checks it again before SIGKILL. If the process ended and the number now belongs to another process, the new process is not signalled.

What it exposes: the command line of each process goes into the tool result, the Inbox notice and the daemon log, cut at 400 characters. A command line can hold a secret (an API key in an argument). It is the same data any user on the machine reads with `ps`, but now it is written to the Inbox and the log too. The gateway event keeps only pids and outcomes. The notice is the record of what was stopped.

### Stopped Claude background trees

When agent-net stops a Claude worker or owner session it runs `claude stop`, records the job's pty host and claimed process, and checks again about ten seconds later ([decision 0109](decisions/0109-end-stopped-claude-background-trees.md)). The check trusts only the supervisor's roster, `~/.claude/daemon/roster.json`: every pid in it is live, whatever `~/.claude/jobs/<id>/state.json` says. The check ends a tree only after a roster at most 60 seconds old no longer lists the job or either recorded pid. A missing or stale roster means it does nothing; `daemon.status.json` is never used.

After that it ends what is left only when the job state is `stopped`, `done` or `failed`, every pid still carries the recorded start time, the tree belongs to the daemon's own user (real and effective), and it is not a registered live agent-net session or worker. The start time is checked again before SIGTERM and before SIGKILL, so a reused process number is not signalled. There is no periodic sweep. Each ended tree writes one line with the job ID, host pid, age and tree RSS to the daemon log.

## Automatic reply reminders

The gateway can send one notification from the reserved `agent-net` identity
when a public request remains unanswered and its recipient is idle. Both the
request and the recipient's last reported activity must be over two minutes
old. A missing activity time prevents a reminder. The minute sampler never
creates a reminder for an offline session. It counts that request instead.
It excludes owner conversations, CTO recipients, notifications, held requests, expired
requests and requests already answered.

A reminder is a notification, not a new request or an approval. It carries no
owner authority. It names the original sender and request ID and asks the
recipient to use `agent_message_reply` on that request, or say it cannot. Sender names
are cleaned before insertion. Ordinary senders cannot use the `agent-net`
identity. Harness delivery recognizes that identity together with its
gateway-supplied harness and notification type, so a peer cannot forge the
system framing with a name, body marker or harness alone.

The gateway checks the original sender-to-recipient edge, with the same
specific-edge-before-wildcard order as a request. An `ask` or `never` rule
blocks the reminder. It checks again before queue delivery, including retries,
and withholds a reminder if the recipient became busy or offline, the request
closed, or the edge changed. It never injects reminders into the private CTO
conversation. Public requests sent by the CTO may remind the peer that owes
an answer. The only new stored fact is the request's `reminded` flag, set
atomically with the normal notification record. A restart cannot create
another reminder for that request. Reminder and answer counters use retained
message history; the count of offline requests skipped is kept in memory.


## Idle checks

The gateway can send one notification from the reserved `agent-net` identity
to an online, idle session that has open work. Open work is a request it owes
an answer, a running worker it started, or an announced task. It never checks
a worker session, the CTO, an owner session or an offline session. It checks
a session at most once per idle period, and at most 6 times a day.

A check is a notification, not a request or an approval, and carries no owner
authority. It asks the session to answer with `agent_announce`: done, more with
one line, or blocked on X. The gateway reads only the next announce. It does
not start or resume a session, and it routes nothing to the CTO. `blocked`
leaves one open row per session in the owner's Desk (tab 7) queue, whatever the
autonomy. The row text is cleaned and the `[agent-net` marker is neutralized.

A node's `autonomy` setting is `off` by default or `wake`. With `wake`, a
`more` answer from an online session gets one short continue notification from
the reserved `agent-net` identity and no Inbox row. An offline session is
not woken: this slice never starts or resumes a session, and creating a new
agent or a clone stays with the owner or CTO. `blocked on X` still
leaves a row. Only the owner changes autonomy, through `node_settings_set`
(owner gate) or the CTO; a session cannot set its own.

Wake-ups have their own cap of 6 per agent per day, separate from the check
cap. Two wakes in a row with no new message and no change in open work stop the
waking and leave one row, `<name> was woken twice with no progress`; a later
change allows waking again. The gateway stores the daily count, the
no-progress streak and the progress markers, never task content. The wake
notification is stored through the normal message queue, so its delivery and
retries use the same checks as any other notification. The operator counts
call includes `wake_ups_today`.

A node's standing loop (`loop_every`, decision 0094) wakes the node's lead on a
clock, so it spends tokens on a schedule. The owner or the CTO sets it, with no
approval step. Its stops reuse the wake rules: the same 6 wakes a day per
session, shared with the idle-check wakes, and a pause after two ticks in a row
with no progress. A pause or the cap opens one Desk ask (`resume` or `stop`).
A tick only goes to an existing session and never starts one, a busy session
is skipped, and Pi is skipped because it cannot be woken. The gateway stores
the clock, the day's counts, the streak, the progress markers and one cleaned
announce line, never task content.

The gateway stores the activity time it has checked, the small stopping
counters for "more", the daily check and wake caps and counts of answer kinds.
It does not store the announced task or the answer line as check state. The
Inbox row does hold the line the agent supplied, like any other inbox item.
Ordinary senders cannot use the `agent-net` identity, so a peer cannot forge a
check or a wake. A check or wake reaches the session's own model, which may use
a paid or protected provider, so the caps and the stopping rules keep both
rare.



Quality weighting is off for good (decision 0105). The methods `quality_get` and
`quality_set` remain owner only on the local socket, but `quality_set` changes
nothing and `quality_get` always answers disabled. The picker reads its own
table of task results (`picker_get`, owner only on the local socket). That table
only decides which allowed profiles rotate. It cannot bypass protected
providers, worker floors or quota exclusions, and an explicit model request
does not use it.

Worker cleanup removes only managed worktrees after the worker has ended. A worker branch is deleted when its delivery change is merged or done and the merge is verified; a stopped change keeps its branch. It checks the default branch and delivery changes, including prior revisions, and applies the retention periods in the reference. A squash merge is a normal commit on the default branch, so the merged commit check accepts it without requiring the branch head to be an ancestor. For a branch with no delivery change, cleanup refreshes `origin/<default>` with a short timeout and accepts a merge, cherry-pick or squash proven on either the local or the remote default branch. A failed fetch never counts as a merge and leaves the refs as they are. It keeps worktrees with uncommitted changes or untracked files, running workers, queued branches and branches used by starts in progress. Ignored files are removed with the worktree. Cleanup logs the reason and bytes removed.

### Tools owner actions

The Tools TUI uses `tools_mcp_add`, `tools_mcp_remove`, `tools_mcp_test`,
`tools_mcp_list`, `tools_import_folder`, `tools_keep_copy`,
`tools_save_changed`, `tools_update_plan` and `tools_update_copies` through the
operator gate. Bound agents and CLI processes inside agent sessions are refused.
Folder imports, copy choices, saving a changed copy and updating copies also carry a gateway-verified owner session. Updating copies replaces only the exact copies the owner confirmed. The machine repeats the checks and refuses a copy that was edited after it was deployed, so a plan that went stale cannot overwrite someone's edit. The old copy is backed up first and restored if the install fails.
A folder import packs only the explicitly chosen folder, using the existing
bounded skill archive and extraction checks. Reading a stored skill uses the
same bounded text reader as an observed file. Neither operation runs scripts.
A copy choice validates all named observations before any removal, imports the
chosen copy first, and uses the host's hash-checked backup journal for the other
native user copies. Tracked, package, plugin and system sources are refused.
A skill switch can also remove an observed unmanaged native copy after the
owner applies the plan. The daemon rechecks its location and content hash,
refuses tracked files and unsafe paths, and moves it to a private backup before
forgetting it. Changed content stops the removal.

### Public apps listener

The public listener trusts only its loopback tunnel to supply the client
address. Tailscale Funnel replaces X-Forwarded-For. Other tunnels must be
configured to overwrite this header, not append to a client-supplied value.
The MCP endpoint checks the configured public host and any Origin header.
It authenticates before reading a request body and limits body size, read time,
concurrent requests, app calls, and MCP sessions. An app may hold ten sessions;
the listener permits 200 in total and closes idle sessions after ten minutes.
Revoking an app closes its sessions.

**A bot with a shared MCP server.** The owner can grant one bot the use of one
shared MCP server (`g` on the bot's row in Connected apps; the grant is
`mcp server <name>`). That bot can then list and call the tools the server
allows (`allowed_tools`) through the public `/mcp`, with the server's own
credentials. This is the owner's explicit choice, per bot and per server, and
nothing grants it by default. A granted bot is a caller you cannot see: a
prompt-injected bot can call those tools too. So grant a server only for tools
you accept that risk for, and narrow it with an allow-list. The header value
or environment value stays on the hosting machine: it is read there, never sent
to the gateway or the bot, and redacted from results as for sessions. The
gateway reads the grants on every request, so removing a grant, revoking the
app, disabling or removing the server stops the calls at once. The project scope
of a server does not apply to a bot (a bot has no project); the grant is the
check. A call counts against the bot's call limit, like any bot call; no other
limit is added. The grant and ungrant operations are owner-only: agents,
including the CTO, cannot make them. Each call is logged as `apps.mcp_server_call`
with the app, server and tool, never the arguments.

**A bot is an agent node** ([decision 0092](decisions/0092-bots-as-agents.md)).
The node gives a bot no permission: its grants and app limits are the only
things that bind it, and a bot reads its charter only if its own instructions
tell it to. A bot can join a Team but can never be a Team or Repo lead, so it
holds no routing or approval authority over other agents. A message from a bot
routes as before, because the node adds no routing identity. The Graph read
that every enrolled machine can make now shows a bot's name, client name, when
it connected and last used, the servers it may call and one derived `calls`
edge per granted server. Those were already visible to the owner only; share
them knowingly. The `calls` edge is derived on read from the grants and cannot
be written, so the Graph edit methods cannot grant a bot anything.

**A bot may act under a name** ([decision 0118](decisions/0118-bot-names.md)).
The name (`as` on a bot tool, the `X-Agent-Net-As` header, or an `as:` client
name) is a label, not a security boundary. All the names of an app share its
token, grants and call and message limits, and a caller that holds the token can
send as, and read the mail of, any name of that app. The name is checked only
for its format (1 to 32 characters: letters, digits, space, `-`, `_`) and a
revoked app cannot get a named session. The number of names is not limited
beyond the app's call limit. To keep two bots apart for real, connect them as
two apps. Revoking an app archives its named nodes; no named session or message
is deleted.

**Its own Tailscale node.** With `agent-net setup --gateway
--apps-tailscale-name NAME` ([decision 0058](decisions/0058-own-tailscale-name.md)),
the gateway runs its own Tailscale node in its process (tsnet) and serves the
connected-apps handler on that node's Funnel listener, port 443 only. The
listener takes only Funnel connections, not tailnet ones. The remote address of
each request is the internet client that Funnel reports; X-Forwarded-For from
that listener is ignored, as from any peer that is not loopback. The phone
cookie and the MCP URL are then on a name that no other service shares. The
same body, time, request and session limits apply. The node's state folder,
`tailscale/` in the gateway data folder, holds the node's private keys: it is
mode 0700, inside the data folder you back up, and nothing prints it. An auth
key comes only from a file path (`--apps-tailscale-authkey-file`) or the
`TS_AUTHKEY` environment variable. Setup saves only the path. The gateway reads
the key only for the first login, and errors name the file, never the key.
Without a key, the login link appears only in the owner's TUI. Whoever opens it
first chooses the tailnet the node joins, so do not share it.
Anyone who can read the state folder can act as that node in your tailnet. It
is a normal tailnet machine, so limit what it may reach with your tailnet
policy, for example with a tag. tsnet sends its logs to Tailscale's log
service, as tailscaled does. Uninstall logs the node out and deletes the folder,
only when it has agent-net's marker.

Each OAuth sign-in requires a fresh code approved by the owner. Unapproved
registrations expire after one hour and are limited to five per source per
hour and 200 total. Pending sign-ins expire after ten minutes and are limited
to three per client, three per source, and 20 total. Oldest unapproved entries
are evicted when a total pool is full. Access tokens are limited to 20 per app.
Refresh tokens rotate; reuse of a consumed token revokes its token family.
Each family may rotate at most 200 times in a 24-hour window starting at its
first rotation. Exceeding the limit revokes the family and requires a new
sign-in. The gateway retains up to 2,000 consumed refresh hashes per app,
deleting the oldest first, and deletes any remaining hashes 90 days after use.
A reused hash that has been removed can no longer trigger family revocation.
Before an authorization code is exchanged, its plaintext is stored temporarily
in the private gateway database so a lost polling response can be retried with
the same code. It expires after 45 seconds, is cleared on exchange or cleanup,
and requires the PKCE verifier. A successful exchange consumes the approval. Active address bans
are stored separately from rate buckets, with at most 50,000 bans retained.

## Known limits

Owner decisions, 2026-11-02:

- The owner-only check for apps.* and every owner action uses process ancestry.
  Code running as the owner's user can escape it or read the gateway database.
  Trust stops at the user account. Every approval is logged and writes a visible
  owner notice.
- The phone cookie is scoped to the host name, not the port. Other HTTPS
  services on that host receive it. Anything able to add such a route already
  runs as the owner. The TUI warns when other routes exist on the host.

Claude Code and Codex sign-in probes run `claude auth status` and `codex login status` with a five second limit. Only the signed-in state and auth method are kept; account details are discarded.

### Antigravity hooks

The plugin runs `agent-net hook antigravity` at session start and on each invocation and stop. A fourth hook, on `PreToolUse` for `call_mcp_tool`, answers `auto_approve` only when the call goes to agent-net's own MCP server (`agent-net_agent-net`), so an agy session does not ask the owner to approve agent-net's tools; it answers nothing for every other tool, which keeps agy's normal approval. It runs locally and does not contact the daemon. The command sends the hook JSON, including the conversation ID, workspace paths, transcript path and last user input when present, to the local daemon socket. The daemon keeps only the latest event, conversation ID, workspace paths, time and stop idle flag in memory. Hook failures return an empty JSON object, and the command has a five second limit. Antigravity MCP calls carry a root conversation ID; it is untrusted and is not bound to a session until discovery can verify it.

### Antigravity main session delivery

The local agy hook reports a conversation ID and workspace. The daemon checks the hook process ancestry, the live agy process and its working folder before discovery. An MCP connection binds only when its conversation ID matches a discovered session and its process descends from that agy process. Known worker and child conversation IDs are excluded from main sessions. The parent check uses agy's read-only summaries database; when that database or column is missing it cannot identify a child from the summary. These checks are local identity checks, not a sandbox against a malicious program running as the same user.

A queued message is returned in a Stop continuation or a PreInvocation user-message injection only to a hook in the discovered process tree. Message framing identifies the sender and whether a request has owner authority. For an idle session in herdr, the daemon sends the framed text and Enter to a pane only after its process information matches the agy process tree or the pane reports that exact agy conversation ID. Messages with terminal control characters wait for a hook instead of being typed. A failed or ambiguous herdr write is reported as failed with a detail saying the message may have arrived. A durable unconfirmed record prevents typing it again. As with normal terminal input, typing can race with the owner starting a turn.

The `worker_log` operator method is owner only. It reads at most 200 recent
worker runs, including assessments and stored notice facts. Agent connections
are refused by the same local owner gate as conversation history.

Worker task messages carry a gateway-checked marker. The worker's host may mark
a task from its owner. When the owner is on another machine, the gateway accepts
the marker only for a start it tracked for that host, owner and worker. Other
markers are stripped. Daemons before 0.4.130 use the worker brief prefix for
compatibility; that older format cannot distinguish a quoted prefix.

Daemon leftover commits exclude files larger than 5 MB, binary executables and build outputs (root agent-net, *.test and *.exe); excluded files remain in the worktree and are listed in the notice.

### Merger registration

Only the owner through the TUI/CLI owner gate or the CTO
may register a project's merger. Ordinary agent sessions and workers cannot
call the owner-only `merger_set` operation. A session cannot become a merger
by announcing a type. The gateway derives that type from its registration.
A registration must name a non-worker Claude session of the same project on
the named machine. Deleting that session record clears the registration;
stopping the harness session or taking it offline keeps the registration and
leaves its tasks waiting. The gateway accepts a cross-machine change when the
caller presents an owner session of its own node. Every enrolled node has such
a session, so this proves only that the caller is an enrolled daemon. It is the
same trust model as graph and settings changes, and no stronger.

`@merger` is a reserved delivery owner for test and merge. Tasks go only to
the registered session for the change's project and machine. Claim and report
check that registration, the authenticated reporting machine, and that the
session is online. Other sessions of the same project cannot take over.
Offline tasks wait. Removing a registration, including by deleting its
session, releases every unfinished `@merger` claim and queued notice and returns
those owners to the caller. This includes after-merge steps and preserves the
accept owner. Replacement releases the old claims and queues tasks for the new
merger. Cleanup runs atomically on the registration record. Completed step
evidence remains tied to its recorded revision and commit.

A CTO `merger_start` starts Claude on the registered project and machine, registers the session, and assigns
test and merge. The built-in brief requires its own merge worktree, excludes
build outputs, binaries and secrets, and tells it to resolve merge conflicts in its own worktree, preserving both
intents and reporting each resolved file. Mechanical migration and decision
renumbers remain a special case. It runs targeted tests and CI after resolution
and reports test failures without rewriting features. These are session
instructions, not a git permission boundary.

Typed Repo/Agent Graph cutover is automatic. No owner request, Settings action
or agent call can trigger it. The gateway checks at start and after each
machine completes its `hello`. It enables typed reads and writes only when
every enrolled machine, including an offline machine or one that has never
connected, has reported the `graph_refs` capability in `hello` with a known
release version. An older or unknown daemon holds the cutover back; the owner
upgrades or removes that machine. The gateway takes a validated backup before
enabling. If the enable fails, the backup stays, the gateway raises one Inbox
item with the error, and the next machine hello or gateway start retries it.
The minute timer raises the 24 hour blocker notice and never retries the
enable. After cutover each new connection must send its own `hello` before it
becomes available for routing. Older and development daemons can connect and
upgrade, but Graph operations require typed Graph capability on that connection
and are refused with `upgrade required` otherwise. Legacy Graph string adapters
remain available to upgraded daemons during the owner surface transition.
Rollback requires restoring the pre-cutover backup.

Explicit Agent selection at session start uses `session.agent.reserve`. The
caller must name the owner pseudo-session on its authenticated machine. The
local daemon exposes this only to its owner start path or the CTO. Agent role text grants no authority. Unknown and tombstoned Agents
are refused before launch and checked again at binding and first registration.
A reservation cannot replace an existing identity or bind a worker.

Before launch, a durable start reference records the Agent and existing session
IDs. New lasting sessions of that harness have no final Agent and cannot
receive messages or resolve their context until the exact returned native ID
is bound. An already registered candidate may be assigned only while held and
only if it was absent from the baseline. Binding and releasing delivery are
transactional. A hold or native reservation awaiting first registration expires
after two minutes, or sooner when the daemon reports failure. The deadline is a
constant, not a configurable setting. Cancellation records the failed start,
releases unrelated sessions with their normal defaults, and creates one owner
Inbox item. A late registration receives the Repo-owner default; a late binding
cannot revive the canceled Agent choice. An exact late ID may update the same
Inbox item, never create a second one or guess an Agent from a directory.
Existing sessions and workers are unaffected. Remote selections require `session_agents` in the
destination's current hello. Old daemons and starts without a selection keep
the existing Repo-owner default.

Team writes and Team history reads require the owner operator channel and an
online owner session on the calling daemon's machine. Team writes also require
the typed Graph cutover. Team ownership and membership grant no authority,
message trust, settings inheritance or delivery assignment. The gateway checks
membership kinds, live Agent endpoints, active Teams and expected old membership
in the write transaction. An owning Agent cannot be deleted or tombstoned while
any Team retains its owner reference, including archived Teams.

Session archive and restore are owner-only operations. Ordinary agent
connections cannot call them. The CTO can archive at once and the owner can
restore archived sessions. The gateway checks owner
authority, refuses online sessions, and updates a batch in one transaction.
Archiving retains rows and historical references; it does not revoke a session.
A live session report restores its visibility.


### Owner session use and pins

`session_pin` uses the owner-only local operator gate. The gateway checks that
the supplied owner session is online on the calling node before setting or
clearing a pin. The same check protects open and resume use reports. Enrolled
nodes are trusted to report their own Claude and Pi input events. Pi accepts
only its bound plugin's input report; Claude's optional policy hook identifies
a local session and ignores known agent-net peer and automated prompt wrappers.
Claude does not document a prompt-source field, so other automated inputs may
still count as use. These signals are not proof of human identity against
another process running as the same user. Use times, scores and pins appear in session listings available to
enrolled agents. Prompt text is not stored by this feature.

Automatic archival changes visibility, not historical records. It excludes
pinned, online, recently owner-used, owner, CTO, bot, and registered merger
sessions, and the owner can restore an archived record. A restore records use
so the automatic job does not immediately archive it again.

Worker retention cleanup deletes harness data only for ended worker records.
Live workers are excluded. Codex deletion uses the recorded thread UUID and the
harness's forced delete command, never a session name or a project-wide delete.
OpenCode deletion runs against the worker's private data directory, never the
owner's shared OpenCode database. Configuration and owner authentication files
are not removed. Failed harness deletion retains the worker record for retry.
Worker records are dropped if harness deletion still fails seven days after
cleanup eligibility. An unsupported Codex delete command also drops the record.
Safety refusals for live workers and invalid identities or data paths never
expire. Deletion runs in a bounded background pass, separate from gateway link
setup and worker health checks.
The daemon logs these skips; an explicit missing-thread response counts as success.
`workers.delete_sessions: false` in node.json disables harness data deletion.


Repo and Team leads affect message routing, not trust or approval. Only the
owner or CTO can set a lead. The gateway checks request
rules against the actual redirected recipient. Owner and CTO messages and a
worker's messages to its caller are not redirected. For an explicit Repo or
Team address, the gateway verifies that the recipient is still that lead before
delivering. The gateway admits agent-net session starts against the Repo's `max_agents` setting and reserves
places for concurrent starts. Terminal sessions and late registrations are
never refused; they can put a Repo over its configured max.

A hand-off (`agent_message_handoff`) is the one message whose stored sender is not the session that called the tool. It keeps the sender of the message being passed on, so the member's reply reaches the asker. The gateway makes it only for the recipient of that message, records the lead in the message's `handed_off_by` field and in a `msg.handoff` event, and sets no flag a sender could set on a normal send. A handed-off request is checked against the request rule from the asker to the member, as a normal request is: `ask` holds it for the owner, `never` refuses it.

**Orders, authority mode and held back (decision 0089).** An order is a message the sender flagged with `order` on `agent_message_send`. The gateway keeps the flag only when it checks the tree and finds the sender above the recipient: a team lead is above every repo, repo lead and agent of its team, and a repo lead is above the other agents that work in its repo. From anyone else, from a reply, or from a bot, the flag is dropped, the message is stored as a request, and the sender's receipt says so. The gateway stores the checked result as `is_order` and writes the order note on every delivery from the live tree, outside the body, so a body cannot supply it, and a lead that has since left the tree loses its label. A reply from a session above the recipient carries the advice note, which says it does not bind. The note names the lead, the team or repo and the team's authority mode, says what the mode means, and always says that deleting data, spending money, and using or sharing secrets that can spend money stay with the owner. The owner changes a team's mode and held back text and a repo's held back text from the Teams list. The CTO sets the same Team fields with `graph_change` `team_charter`, `attr: mode` or `attr: held_back`, at once as an owner action under the private owner operations above; graph undo restores the earlier value. The held back text is free text for agents to read. No code reads it as a grant or a limit, and `rules` text never grants or removes authority. Delivery approvals for `delegated` merges go to the repo lead and the lead of the repo's team, never to the agent that made the change.

Idle Claude compaction types `/compact` only into an idle background session
with a job ID after the configured idle time and context threshold. It excludes
running `claude attach <jobId>` clients using the process list, checks again
before typing, and skips compaction if that check fails. A client can still
attach between the last check and the write. The daemon stops only its own
attach client, not the background process. See decision 0078.

## Inherited charter values

Global and Team charter writes use the existing owner/CTO rule gate. Repo and
Agent charter writes retain their existing gates. Reading an effective charter
is public and includes the source of each field. All inherited refuses remain
visible even when an Agent has its own refusal. Free-text rules grant no tool
rights. An absent charter adds no limit, deploy approval or charter prompt.
The removed consumers, needs and notes attributes cannot be written.

## Standing orders

The CTO reads the owner's standing orders as instructions at the start of every conversation. The gateway stores them. The CTO can save and remove them on the owner's instruction, and every save or removal leaves an Inbox notice with an undo. The notice and the TUI list show the source, `owner` or `cto`. A machine that is enrolled can send the frames, so the gateway trusts the daemons of your machines, as it does for every other frame. A prompt-injected CTO could save a false order: the list in Settings shows each order and its source, and the owner can remove one at any time.

Topic subscriptions on the daemon socket use the owner gate: agent clients and
commands in an agent session's process tree are refused. The gateway accepts
the subscription from an enrolled daemon, like other node frames. Events carry
only topic names and version numbers, not the data in the corresponding reads.
