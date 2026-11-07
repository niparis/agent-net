# 0081: Gateway step contracts

The gateway already chooses who owns each delivery step. It also owns what the
step means. Private caller notes and a fixed merger prompt drifted apart: callers
repeated test suites during accept, and mergers refused conflicts they should resolve.

Each step has a live advice setting, `delivery.contract.<step>`, at network and
project level. A project value replaces the network default. Owners edit these in
Settings; the CTO uses the existing settings tools. The next task reads the current
value. Each task includes a `contract:` block immediately after its step line,
including batched tasks and reminders.

The merger prompt retains identity, safety and batching instructions. Step tasks
supply its contracts. Worker notices name the test contract so callers can explain
the expected test scope in briefs. Accept and review read diffs without running
suites; the test step owns tests and CI.
