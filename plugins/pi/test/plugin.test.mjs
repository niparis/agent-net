// Tests for plugins/pi/agent-net.ts with a fake Pi extension API and a fake daemon.
// Usage: node plugin.test.mjs <path to agent-net.ts> <dir with typescript installed>
// Run it through plugins/pi/test.sh.
import { createServer } from "node:net";
import { readFileSync, writeFileSync, mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const [pluginPath, tsDir] = process.argv.slice(2);
const ts = createRequire(join(tsDir, "x.js"))("typescript");

const dir = mkdtempSync(join(tmpdir(), "piplugin-"));
process.env.AGENT_NET_SOCKET = join(dir, "d.sock");

// The extension needs `typebox` at run time (schemas only); a stub is enough here.
mkdirSync(join(dir, "node_modules", "typebox"), { recursive: true });
writeFileSync(join(dir, "node_modules", "typebox", "package.json"), JSON.stringify({ name: "typebox", type: "module", main: "index.js" }));
writeFileSync(
  join(dir, "node_modules", "typebox", "index.js"),
  "const f = () => ({});\nexport const Type = new Proxy({}, { get: () => f });\n",
);

const js = ts.transpileModule(readFileSync(pluginPath, "utf8"), {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
}).outputText;
const jsPath = join(dir, "agent-net.mjs");
writeFileSync(jsPath, js);
const extension = (await import(pathToFileURL(jsPath).href)).default;

// Fake daemon.
const hellos = [];
const statuses = [];
const textResults = [];
let conn;
const server = createServer((c) => {
  conn = c;
  let buf = "";
  c.setEncoding("utf8");
  c.on("data", (chunk) => {
    buf += chunk;
    let nl;
    while ((nl = buf.indexOf("\n")) >= 0) {
      const line = JSON.parse(buf.slice(0, nl));
      buf = buf.slice(nl + 1);
      if (line.method === "hello") hellos.push(line.params);
      if (line.method === "status") statuses.push(line.params);
      if (line.method === "session_text_result") textResults.push(line.params);
      c.write(JSON.stringify({ id: line.id, result: {} }) + "\n");
    }
  });
});
await new Promise((resolve) => server.listen(process.env.AGENT_NET_SOCKET, resolve));

async function until(what, cond, ms = 3000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (cond()) return;
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  throw new Error("timed out waiting for: " + what);
}

let failures = 0;
async function test(name, fn) {
  try {
    await fn();
    console.log("ok   " + name);
  } catch (error) {
    failures++;
    console.log("FAIL " + name + ": " + error.message);
  }
}

// A fake Pi: records handlers so the test can fire events.
function makePi() {
  const handlers = new Map();
  return {
    handlers,
    api: {
      on(event, fn) {
        handlers.set(event, fn);
        return () => {};
      },
      registerTool() {},
      sendMessage() {},
    },
    fire: (event, ...args) => handlers.get(event)?.(...args),
  };
}

const userMsg = (id, parent, text) => ({ type: "message", id, parentId: parent, timestamp: "2026-10-29T10:00:00.000Z", message: { role: "user", content: text } });

function branchFixture() {
  return [
    { type: "thinking_level_change", id: "e0", parentId: null, timestamp: "2026-10-29T09:59:00.000Z", thinkingLevel: "off" },
    userMsg("e1", "e0", "fix the build"),
    {
      type: "message", id: "e2", parentId: "e1", timestamp: "2026-10-29T10:00:01.000Z",
      message: {
        role: "assistant",
        content: [
          { type: "thinking", thinking: "hmm" },
          { type: "text", text: "Looking." },
          { type: "toolCall", id: "t1", name: "bash", arguments: { command: "go build ./..." } },
        ],
      },
    },
    { type: "message", id: "e3", parentId: "e2", timestamp: "2026-10-29T10:00:02.000Z",
      message: { role: "toolResult", toolCallId: "t1", toolName: "bash", content: [{ type: "text", text: "ok" }], isError: false } },
    { type: "message", id: "e4", parentId: "e3", timestamp: "2026-10-29T10:00:03.000Z",
      message: { role: "bashExecution", command: "ls", output: "a b" } },
    { type: "message", id: "e5", parentId: "e4", timestamp: "2026-10-29T10:00:04.000Z",
      message: { role: "custom", customType: "agent-net", display: true, content: "hello from a peer" } },
    { type: "message", id: "e6", parentId: "e5", timestamp: "2026-10-29T10:00:05.000Z",
      message: { role: "toolResult", toolCallId: "t2", toolName: "read", content: [{ type: "text", text: "no such file" }], isError: true } },
    userMsg("e7", "e6", "x".repeat(5000)),
    { type: "label", id: "e8", parentId: "e7", timestamp: "2026-10-29T10:00:06.000Z", targetId: "e7", label: "x" },
  ];
}

function sessionCtx(extra = {}) {
  return {
    cwd: "/work/p",
    mode: "tui",
    model: { provider: "anthropic", id: "claude-sonnet-5" },
    sessionManager: { getSessionId: () => "pi-session-1", getBranch: () => branchFixture() },
    ...extra,
  };
}

function pushText(params) {
  conn.write(JSON.stringify({ method: "session_text", params }) + "\n");
}

await test("hello carries the mode and the model", async () => {
  const p = makePi();
  extension(p.api);
  await p.fire("session_start", { type: "session_start", reason: "startup" }, sessionCtx());
  await until("hello", () => hellos.length === 1);
  const h = hellos[0];
  if (h.mode !== "foreground" || h.model !== "anthropic/claude-sonnet-5" || h.native_id !== "pi-session-1") {
    throw new Error("hello: " + JSON.stringify(h));
  }
  await p.fire("session_shutdown", {});
});

await test("Pi's placeholder model unknown/unknown is not reported as a model", async () => {
  const p = makePi();
  extension(p.api);
  await p.fire("session_start", { type: "session_start", reason: "startup" }, sessionCtx({ model: { provider: "unknown", id: "unknown" } }));
  await until("hello", () => hellos.length === 2);
  if ("model" in hellos[1]) throw new Error("hello: " + JSON.stringify(hellos[1]));
  await p.fire("session_shutdown", {});
});

await test("session_text answers with the conversation, newest last", async () => {
  const p = makePi();
  extension(p.api);
  await p.fire("session_start", { type: "session_start", reason: "startup" }, sessionCtx());
  await until("hello", () => hellos.length === 3);
  pushText({ req: "r1", limit: 50 });
  await until("answer r1", () => textResults.some((r) => r.req === "r1"));
  const r = textResults.find((x) => x.req === "r1");
  if (!r.ok) throw new Error("not ok: " + r.error);
  const got = r.page.entries.map((e) => `${e.role}|${e.tool ?? ""}|${e.text.slice(0, 30)}`);
  const want = [
    "user||fix the build",
    "assistant||Looking.",
    "tool_call|bash|bash: go build ./...",
    "tool_result|bash|ok",
    "tool_call|bash|bash: ls",
    "tool_result|bash|a b",
    "user||hello from a peer",
    "tool_result|read|error: no such file",
  ];
  if (JSON.stringify(got.slice(0, 8)) !== JSON.stringify(want)) throw new Error("entries: " + JSON.stringify(got));
  const last = r.page.entries[r.page.entries.length - 1];
  if (last.role !== "user" || !last.cut || Array.from(last.text).length !== 2000) throw new Error("long prompt not cut: " + last.text.length);
  if (r.page.entries[0].time !== Date.parse("2026-10-29T10:00:00.000Z")) throw new Error("time: " + r.page.entries[0].time);
  await p.fire("session_shutdown", {});
});

await test("session_text pages back and forward, and says why it failed", async () => {
  const p = makePi();
  extension(p.api);
  await p.fire("session_start", { type: "session_start", reason: "startup" }, sessionCtx());
  await until("hello", () => hellos.length === 4);
  const ask = async (id, params) => {
    pushText({ req: id, ...params });
    await until("answer " + id, () => textResults.some((r) => r.req === id));
    return textResults.find((r) => r.req === id);
  };
  const texts = (r) => r.page.entries.map((e) => e.role + ":" + e.text.slice(0, 12)).join(",");
  const newest = await ask("a", { limit: 2 });
  if (newest.page.entries.length !== 2 || !newest.page.older || !newest.page.newer) throw new Error("newest: " + texts(newest));
  const older = await ask("b", { limit: 2, before: newest.page.older });
  if (older.page.entries.length !== 2) throw new Error("older: " + texts(older));
  const seen = new Set([...older.page.entries, ...newest.page.entries].map((e) => e.cursor));
  if (seen.size !== 4) throw new Error("pages overlap or repeat");
  const none = await ask("c", { after: newest.page.newer });
  if (none.page.entries.length !== 0 || none.page.newer !== newest.page.newer) throw new Error("nothing new expected");
  const gone = await ask("d", { before: "nope:0" });
  if (gone.page.entries.length !== 0) throw new Error("unknown cursor should give nothing");
  // A session whose history cannot be read.
  await p.fire("session_start", { type: "session_start", reason: "new" }, sessionCtx({ sessionManager: { getSessionId: () => "pi-session-1" } }));
  const failed = await ask("e", {});
  if (failed.ok || !failed.error) throw new Error("failure: " + JSON.stringify(failed));
  await p.fire("session_shutdown", {});
});

server.close();
rmSync(dir, { recursive: true, force: true });
if (failures) {
  console.log(failures + " test(s) failed");
  process.exit(1);
}
console.log("all tests passed");
process.exit(0);
