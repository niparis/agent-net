# Worker brief template

This is a template, not a rule. `agent_worker_start` does not check or score
the brief's format (decision 0145). A brief missing any section below starts
the same as one that has them all.

The one brief check left is overlap: a brief may not name a file that a running
worker or an open change in the same project is already working on, unless it
says `overlap ok: <reason>`. A change or worker the start itself targets is not
an overlap: its id in the brief, an open change whose branch is the start's
`base`, or the `continue` or `fixes` target. Use `mode: "check"` to run that
check and start nothing.

The analysis behind decision 0145 found that briefs failing more of the old
format rules did slightly better, not worse. The sections below are still a
useful habit: they answer what a worker needs to know. Write what helps your
worker, and leave out what does not.

## The sections, if you use them

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