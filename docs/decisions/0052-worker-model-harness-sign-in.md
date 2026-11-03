# 0052: Explicit worker models use an available harness

A model equal to a worker profile name, ignoring case, selects that profile's harness, model and effort. A conflicting explicit harness is refused.

For an explicit model without a harness, resolve the model against the target machine's inventories. Skip harnesses known to be signed out. Prefer a signed-in harness over one with unknown sign-in state. Keep the existing subscription, exact-model and inventory order for ties. The model note names the chosen harness and its sign-in state.

The daemon persists the cleaned model failure detail and sends it with the worker's end report. The gateway extends the existing model failure record with an auth failure timestamp. HTTP 401, HTTP 403 and "not logged in" block that harness and provider on that machine until a later worker succeeds on the same provider. Other failures keep the existing temporary model ranking penalty. Provider IDs stay separate even when their quota aliases match.

Profile availability uses live gateway connections to mark machines online. Stored node inventories alone do not carry online state. A known signed-out harness does not make a profile available.
