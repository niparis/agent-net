# Worker brief template

Every `agent_worker_start` brief has six items. The gateway checks them before
it starts a worker. A brief that misses one is refused with a list of the
missing items, and the caller can fix it and call again. The third attempt on
the same task starts anyway; what is still missing is recorded as a brief
defect against the caller.

Use `mode: "check"` to run the checks and start nothing.

## The six items

1. **Goal:** one line naming who asked. Quote the owner or requester, give a
   message id such as `m_86b8d8cd77f796f0`, or say `owner`.
2. **Files or areas:** at least one repository path, such as
   `internal/gateway/brief.go` or `docs/`.
3. **Limits:** a `Limits:` section, or the test scope and the real setup in
   words, for example "tests use temp dirs and fakes only, never the real
   setup".
4. **Definition of done and How you are graded:** both lines, in the caller's
   words.
5. **Evidence:** an `Evidence:` section with at least three lines.
6. **Overlap:** no file shared with a running worker's brief or an open
   change's changed files in the same project. If the overlap is intended,
   say `overlap ok: <reason>`.

A simple or trivial task from the difficulty judge must also name a model or
pass `not_family`; otherwise the start is refused with
`simple task: name a model or exclude families`.

## Example

```text
Goal: The owner asked: "fix the parser crash" (message m_86b8d8cd77f796f0).
Files or areas: internal/parser/parse.go and internal/parser/parse_test.go.
Limits: tests use temp dirs and fakes only, never the real setup.
Definition of done: the new test fails before the fix and passes after it.
How you are graded: the caller reruns the parser tests and reads the diff.
Evidence:
- the owner's words above
- the crashing input from the issue
- the test that fails today
Overlap ok: the other worker only reads internal/parser/parse.go.
```