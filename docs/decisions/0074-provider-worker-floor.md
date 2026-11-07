# 0074. Provider quota floor for workers

The owner may set, per provider, a minimum share of remaining quota that workers
must leave untouched. When a provider's remaining quota drops below that floor,
agent-net stops picking that provider for workers so the owner retains quota for
interactive chat.

The setting is stored in the gateway's owner settings table under worker_floors
and registered in the settings ledger as providers.worker_floor. Changing it is
owner-only, matching the gate for protected providers. Values range from 0 to
95 percent in increments of 5, where 0 turns the floor off.

The automatic worker picker checks each candidate's provider against known quota
windows. If any tracked window (such as a 5-hour or weekly window) has remaining
quota below the floor, the candidate is excluded from automatic picks entirely,
even as a last resort. The candidate's exclusion is explained in model_note:
"skipped <name>: <provider> has <N>% left in its <window> window, under your <F>%
worker floor". If the provider has no quota reading, the floor is not applied and
model_note records "no quota reading, floor not applied".

An explicit worker start naming a model on a provider under its floor is refused
with that same sentence, unless the caller is the owner's TUI. Starts initiated
by the CTO follow the refusal rule. Running workers are not stopped when a floor
is crossed; only new starts are affected.

The TUI Quotas tab displays configured floors in a FLOOR column after PLAN. Pressing
f opens an inline number entry to set the floor for the selected provider (0 to 95
percent, 0 turns it off). When a provider has a window under its floor, that window
shows its remaining quota in warning color and displays "under floor" in its PACE cell.
