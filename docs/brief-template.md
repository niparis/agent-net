# Worker brief template

A brief is structured. Pass it to `agent_worker_start` as named fields, not as
one block of text (decisions 0145 and 0146). agent-net renders the fields for
the worker in a fixed order: Problem, Evidence, Done, Hypothesis, Scope, Notes.

The format is not checked or scored. A brief that leaves out an optional field
starts the same as one that has all of them. The fields are a habit that
answers what a worker needs to know. Write what helps your worker and leave
out what does not.

## The fields

1. **`problem` (required).** What is wrong or wanted, in the requester's own
   words, and who asked. Quote the owner or requester, give a message id such
   as `m_86b8d8cd77f796f0`, or say `owner`.
2. **`done` (required).** The outcomes the worker can check by itself, one line
   each. The accept and review steps check exactly this list, so write each
   line so that it can be true or false.
3. **`evidence` (optional).** The facts you already have, one line each: the
   failing test, the error text, the commit, the log line.
4. **`hypothesis` (optional).** Your guess at the cause or the fix. The brief
   marks it as yours, and the worker may drop it if it is wrong.
5. **`scope` (optional).** What not to touch and the known traps, for example
   "tests use temp dirs and fakes only, never the real setup".
6. **`task` (optional).** Extra notes only. A call with only `task` still
   works for older callers.

## The one refusal

Only file overlap refuses a start. A brief is refused when it names a file
that a running worker or an open change in the same project is already working
on. If the overlap is intended, say `overlap ok: <reason>` in any field, for
example in `scope`.

A change or worker the start itself targets is not an overlap: its id in the
brief, an open change whose branch is the start's `base`, or the `continue` or
`fixes` target. Use `mode: "check"` to run the overlap check and start
nothing.

## Example

```text
problem: The owner asked: "fix the parser crash" (message m_86b8d8cd77f796f0).
evidence:
- the crashing input from the issue
- the test that fails today
done:
- the new test fails before the fix and passes after it
- the parser tests pass
hypothesis: the crash is an unchecked empty token list in internal/parser/parse.go.
scope: internal/parser only. Tests use temp dirs and fakes, never the real setup.
  overlap ok: the other worker only reads internal/parser/parse.go.
```
