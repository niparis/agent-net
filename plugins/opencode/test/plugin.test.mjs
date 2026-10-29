// Tests for plugins/opencode/agent-net.ts with a fake OpenCode context and a fake daemon.
// Usage: node plugin.test.mjs <path to agent-net.ts> <dir with typescript installed>
// Run it through plugins/opencode/test.sh.
import { createServer } from "node:net";
import { readFileSync, writeFileSync, mkdtempSync, rmSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const [pluginPath, tsDir] = process.argv.slice(2);
const ts = createRequire(join(tsDir, "x.js"))("typescript");

const dir = mkdtempSync(join(tmpdir(), "ocplugin-"));
process.env.AGENT_NET_SOCKET = join(dir, "d.sock");

// Transpile the plugin (types are erased, so no OpenCode packages are needed).
const js = ts.transpileModule(readFileSync(pluginPath, "utf8"), {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
}).outputText;
const jsPath = join(dir, "agent-net.mjs");
writeFileSync(jsPath, js);
const plugin = (await import(pathToFileURL(jsPath).href)).default;

// Fake daemon: records the session IDs that said hello.
const hellos = [];
const statuses = []; // status calls: { id, params }
const closed = [];
const calls = []; // [method, params] of every call except hello
const conns = new Map(); // session ID -> its latest daemon connection
const textResults = []; // params of every session_text_result
const server = createServer((conn) => {
  let buf = "";
  conn.setEncoding("utf8");
  conn.on("data", (chunk) => {
    buf += chunk;
    let nl;
    while ((nl = buf.indexOf("\n")) >= 0) {
      const line = JSON.parse(buf.slice(0, nl));
      buf = buf.slice(nl + 1);
      if (line.method === "hello") {
        conn.sessionID = line.params.native_id;
        conns.set(line.params.native_id, conn);
        hellos.push({ id: line.params.native_id, cwd: line.params.cwd, model: line.params.model, mode: line.params.mode,
          name: line.params.name, status: line.params.status });
      }
      if (line.method !== "hello") calls.push([line.method, line.params]);
      if (line.method === "session_text_result") textResults.push(line.params);
      if (line.method === "status") statuses.push({ id: conn.sessionID, params: line.params });
      const result =
        line.method === "project_add"
          ? { machine: "mac", dry_run: !!line.params.dry_run,
              added: [{ name: "alpha", path: "/c/alpha", remote: "https://example.com/alpha.git" }],
              existing: [{ name: "beta", path: "/c/beta" }],
              skipped: [{ path: "/c/notes", reason: "not a git working tree" }], overflow: 2 }
        : line.method === "projects"
          ? { projects: [
              { name: "alpha", hostname: "mac", path: "/c/alpha", source: "both", remote: "https://example.com/alpha.git", harnesses: ["claude"] },
              { name: "beta", hostname: "mac", path: "/c/beta", source: "registered", harnesses: [] },
              { name: "old", hostname: "box", path: "/c/old", harnesses: ["pi"] },
            ] }
        : line.method === "worker_start"
          ? { session_id: "s_worker", harness: line.params.harness, model: line.params.model, project: line.params.project, hostname: "mac" }
        : line.method === "worker_handback"
          ? { status: line.params.status || "done" }
        : line.method === "worker_stop"
          ? { session: line.params.session, stopped: true }
        : line.method === "nodes"
          ? { gateway_version: "v1", nodes: [
              { node_id: "n_1", hostname: "mac", version: "v0.4.0", online: true,
                harnesses: [{ name: "opencode", path: "/u/opencode", version: "v2", models: ["a/x", "b/y"] }] },
              { node_id: "n_2", hostname: "old", version: "v0.3.2", online: false, harnesses: null },
            ] }
          : {};
      conn.write(JSON.stringify({ id: line.id, result }) + "\n");
    }
  });
  conn.on("close", () => closed.push(conn.sessionID));
});
await new Promise((resolve) => server.listen(process.env.AGENT_NET_SOCKET, resolve));

// Fake OpenCode context. `sessions` is the store behind session.list / session.get.
function makeContext(directory, sessions, hooks = {}) {
  const queue = [];
  let wake;
  return {
    emit(event) {
      queue.push(event);
      wake?.();
    },
    ctx: {
      location: { directory },
      session: {
        async list(input) {
          hooks.onList?.(input);
          const all = [...sessions.values()].filter((s) => !s.parentID);
          return { data: input?.directory ? all.filter((s) => s.location.directory === input.directory) : all };
        },
        async get({ sessionID }) {
          hooks.onGet?.(sessionID);
          const s = sessions.get(sessionID);
          if (!s) throw new Error("session not found");
          return s;
        },
        async prompt(input) {
          hooks.onPrompt?.(input);
        },
        async context({ sessionID }) {
          hooks.onContext?.(sessionID);
          if (hooks.contextFails) throw new Error("context failed");
          return hooks.context?.[sessionID] ?? [];
        },
      },
      event: {
        async *subscribe({ signal }) {
          while (!signal.aborted) {
            while (queue.length) yield queue.shift();
            await new Promise((resolve) => {
              wake = resolve;
              signal.addEventListener("abort", resolve, { once: true });
            });
          }
        },
      },
      tool: { transform(fn) { hooks.onTransform?.(fn); } },
    },
  };
}

const session = (id, dir, ageMs = 0, extra = {}) => ({
  id,
  location: { directory: dir },
  time: { created: Date.now() - ageMs, updated: Date.now() - ageMs },
  ...extra,
});

async function until(what, cond, ms = 2000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (cond()) return;
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  throw new Error("timed out waiting for: " + what);
}
const registered = (id) => hellos.some((h) => h.id === id);
const settle = () => new Promise((resolve) => setTimeout(resolve, 300));

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

// Each test starts from an empty hub, like a fresh process.
function freshHub() {
  delete globalThis[Symbol.for("agent-net.opencode.hub.v1")];
}

await test("session.created registers a session before any prompt", async () => {
  freshHub();
  const sessions = new Map();
  const a = makeContext("/work/a", sessions);
  const stop = await plugin.setup(a.ctx);
  sessions.set("ses_new", session("ses_new", "/work/a"));
  a.emit({ type: "session.created", data: { sessionID: "ses_new" } });
  await until("hello for ses_new", () => registered("ses_new"));
  stop();
});

await test("session.created registers a session that is not readable yet", async () => {
  freshHub();
  const sessions = new Map();
  const a = makeContext("/work/a", sessions);
  const stop = await plugin.setup(a.ctx);
  a.emit({ type: "session.created", data: { sessionID: "ses_late" } });
  await settle(); // the first reads fail
  setTimeout(() => sessions.set("ses_late", session("ses_late", "/work/a")), 100);
  await until("hello for ses_late", () => registered("ses_late"));
  stop();
});

await test("startup discovery registers unused sessions of other directories", async () => {
  freshHub();
  const sessions = new Map([["ses_other_dir", session("ses_other_dir", "/work/b", 30 * 60 * 1000)]]);
  const a = makeContext("/work/a", sessions);
  const stop = await plugin.setup(a.ctx);
  await until("hello for ses_other_dir", () => registered("ses_other_dir"));
  stop();
});

await test("startup discovery skips old, archived and sub-agent sessions", async () => {
  freshHub();
  const sessions = new Map([
    ["ses_old", session("ses_old", "/work/a", 3 * 24 * 60 * 60 * 1000)],
    ["ses_90min", session("ses_90min", "/work/a", 90 * 60 * 1000)],
    ["ses_arch", session("ses_arch", "/work/a", 0, { time: { updated: Date.now(), archived: Date.now() } })],
    ["ses_child", session("ses_child", "/work/a", 0, { parentID: "ses_new2" })],
    ["ses_new2", session("ses_new2", "/work/a")],
  ]);
  const a = makeContext("/work/a", sessions);
  const stop = await plugin.setup(a.ctx);
  await until("hello for ses_new2", () => registered("ses_new2"));
  await settle();
  for (const id of ["ses_old", "ses_90min", "ses_arch", "ses_child"]) {
    if (registered(id)) throw new Error(id + " must not be registered");
  }
  stop();
});

await test("when the owning instance goes away, another one takes its sessions over", async () => {
  freshHub();
  const sessions = new Map([["ses_keep", session("ses_keep", "/work/b")]]);
  const a = makeContext("/work/a", sessions);
  const stopA = await plugin.setup(a.ctx);
  await until("hello for ses_keep", () => registered("ses_keep"));
  const b = makeContext("/work/b", sessions);
  const stopB = await plugin.setup(b.ctx);
  const before = hellos.filter((h) => h.id === "ses_keep").length;
  stopA(); // owner of ses_keep; B must re-register it without any event
  await until("second hello for ses_keep", () => hellos.filter((h) => h.id === "ses_keep").length > before);
  stopB();
});

await test("a hub left by an older plugin version (no instances field) is upgraded", async () => {
  // Hot reload: the old version made the hub without `instances`.
  let stopped = false;
  globalThis[Symbol.for("agent-net.opencode.hub.v1")] = {
    links: new Map([["ses_stale", { link: { stop: () => (stopped = true) }, owner: Symbol("old") }]]),
    rootOf: new Map(),
    gone: new Set(),
  };
  const sessions = new Map([["ses_after_reload", session("ses_after_reload", "/work/a")]]);
  const a = makeContext("/work/a", sessions);
  const stop = await plugin.setup(a.ctx);
  await until("hello for ses_after_reload", () => registered("ses_after_reload"));
  if (!stopped) throw new Error("stale link of the old version was not stopped");
  stop();
});

await test("agent_nodes lists machines with their harnesses", async () => {
  freshHub();
  const sessions = new Map([["ses_n", session("ses_n", "/work/n")]]);
  let tools;
  const c = makeContext("/work/n", sessions, {
    onTransform(fn) {
      tools = [];
      fn({ add: (t) => tools.push(t) });
    },
  });
  const stop = await plugin.setup(c.ctx);
  await until("hello for ses_n", () => registered("ses_n"));
  const tool = tools.find((t) => t.name === "agent_nodes");
  if (!tool) throw new Error("agent_nodes is not registered");
  const out = (await tool.execute({}, { sessionID: "ses_n" })).content;
  for (const want of ["mac (n_1, online, agent-net v0.4.0): opencode v2 (2 models) at /u/opencode",
                      "old (n_2, offline, agent-net v0.3.2): harnesses not reported (older agent-net)"]) {
    if (!out.includes(want)) throw new Error("missing " + JSON.stringify(want) + " in " + JSON.stringify(out));
  }
  stop();
});

await test("agent_project_add and agent_project_list reach the daemon and print a short result", async () => {
  freshHub();
  const sessions = new Map([["ses_p", session("ses_p", "/work/p")]]);
  let tools;
  const c = makeContext("/work/p", sessions, {
    onTransform(fn) {
      tools = [];
      fn({ add: (t) => tools.push(t) });
    },
  });
  const stop = await plugin.setup(c.ctx);
  await until("hello for ses_p", () => registered("ses_p"));
  const add = tools.find((t) => t.name === "agent_project_add");
  const list = tools.find((t) => t.name === "agent_project_list");
  if (!add || !list) throw new Error("project tools are not registered");
  for (const bad of ["on", "machine", "node", "node_id", "host"]) {
    if (bad in add.input.properties) throw new Error("agent_project_add lets the agent pick a machine: " + bad);
  }
  const before = calls.length;
  const out = (await add.execute({ dir: "~/code", scan: true, dry_run: true, depth: 2 }, { sessionID: "ses_p" })).content;
  const [method, params] = calls[before];
  if (method !== "project_add" || params.dir !== "~/code" || params.scan !== true || params.dry_run !== true || params.depth !== 2) {
    throw new Error("wrong call: " + JSON.stringify(calls[before]));
  }
  for (const want of ["Dry run, nothing changed. Would add 1 on mac; 1 already registered; 1 skipped.", "would add  alpha  /c/alpha",
                      "exists  beta  /c/beta", "skipped  /c/notes: not a git working tree", "2 more repositories were found"]) {
    if (!out.includes(want)) throw new Error("missing " + JSON.stringify(want) + " in " + JSON.stringify(out));
  }
  const real = (await add.execute({ paths: ["~/code/alpha"] }, { sessionID: "ses_p" })).content;
  if (!real.startsWith("Added 1 on mac") || !real.includes("workers may start sessions")) throw new Error("real run: " + real);
  const listed = (await list.execute({ name: "alpha" }, { sessionID: "ses_p" })).content;
  const lastCall = calls[calls.length - 1];
  if (lastCall[0] !== "projects" || lastCall[1].name !== "alpha") throw new Error("list call: " + JSON.stringify(lastCall));
  for (const want of ["alpha on mac: /c/alpha  [both]  https://example.com/alpha.git  harnesses: claude",
                      "beta on mac: /c/beta  [registered]", "old on box: /c/old  [seen]"]) {
    if (!listed.includes(want)) throw new Error("missing " + JSON.stringify(want) + " in " + JSON.stringify(listed));
  }
  stop();
});

await test("the model goes in hello, and a model change is sent as status", async () => {
  freshHub();
  const sessions = new Map([
    ["ses_m", session("ses_m", "/work/m", 0, { model: { id: "claude-sonnet-5", providerID: "anthropic" } })],
  ]);
  const m = makeContext("/work/m", sessions);
  const stop = await plugin.setup(m.ctx);
  await until("hello for ses_m", () => registered("ses_m"));
  const hello = hellos.find((h) => h.id === "ses_m");
  if (hello.model !== "anthropic/claude-sonnet-5") throw new Error("hello model: " + hello.model);
  // OpenCode has no reliable foreground signal, so the plugin says "unknown" and never guesses.
  if (hello.mode !== "unknown") throw new Error("hello mode: " + hello.mode);
  m.emit({ type: "session.model.selected", data: { sessionID: "ses_m", model: { id: "gpt-6", providerID: "openai" } } });
  await until("status with the new model", () =>
    statuses.some((s) => s.id === "ses_m" && s.params.model === "openai/gpt-6"));
  // The same model again is not sent twice.
  const n = statuses.length;
  m.emit({ type: "session.step.started", data: { sessionID: "ses_m", model: { id: "gpt-6", providerID: "openai" } } });
  await settle();
  if (statuses.length !== n) throw new Error("an unchanged model was sent again");
  // A step with another model (after a switch the plugin did not see) is picked up.
  m.emit({ type: "session.step.started", data: { sessionID: "ses_m", model: { id: "o9", providerID: "openai" } } });
  await until("status from a step", () => statuses.some((s) => s.id === "ses_m" && s.params.model === "openai/o9"));
  stop();
});

await test("a sub-agent session does not change the model of its root session", async () => {
  freshHub();
  const sessions = new Map([
    ["ses_root", session("ses_root", "/work/r", 0, { model: { id: "big", providerID: "p" } })],
    ["ses_kid", session("ses_kid", "/work/r", 0, { parentID: "ses_root", model: { id: "small", providerID: "p" } })],
  ]);
  const r = makeContext("/work/r", sessions);
  const stop = await plugin.setup(r.ctx);
  await until("hello for ses_root", () => registered("ses_root"));
  r.emit({ type: "session.step.started", data: { sessionID: "ses_kid", model: { id: "small", providerID: "p" } } });
  await settle();
  if (statuses.some((s) => s.id === "ses_root" && s.params.model === "p/small")) {
    throw new Error("the sub-agent model leaked to the root session");
  }
  stop();
});

// ---- dormant state, names, stale links ----

const statusOf = (id) => statuses.filter((s) => s.id === id && s.params.status).map((s) => s.params.status);
const lastStatus = (id) => statusOf(id).at(-1);

async function withDormantAfter(ms, fn) {
  process.env.AGENT_NET_OPENCODE_DORMANT_MS = String(ms);
  try {
    await fn();
  } finally {
    delete process.env.AGENT_NET_OPENCODE_DORMANT_MS;
  }
}

await test("a session with no activity goes dormant and stays registered", async () => {
  await withDormantAfter(300, async () => {
    freshHub();
    const sessions = new Map([["ses_quiet", session("ses_quiet", "/work/q")]]);
    const q = makeContext("/work/q", sessions);
    const stop = await plugin.setup(q.ctx);
    await until("hello for ses_quiet", () => registered("ses_quiet"));
    if (hellos.find((h) => h.id === "ses_quiet").status !== "idle") throw new Error("hello must say idle");
    await until("dormant status", () => lastStatus("ses_quiet") === "dormant");
    const closedBefore = closed.filter((id) => id === "ses_quiet").length;
    await settle();
    if (closed.filter((id) => id === "ses_quiet").length !== closedBefore) throw new Error("a dormant session was disconnected");
    stop();
  });
});

await test("activity wakes a dormant session, and the clock starts again", async () => {
  await withDormantAfter(300, async () => {
    freshHub();
    const sessions = new Map([["ses_wake", session("ses_wake", "/work/w")]]);
    const w = makeContext("/work/w", sessions);
    const stop = await plugin.setup(w.ctx);
    await until("dormant", () => lastStatus("ses_wake") === "dormant");
    w.emit({ type: "session.viewed", data: { sessionID: "ses_wake" } });
    await until("idle again", () => lastStatus("ses_wake") === "idle");
    await until("dormant a second time", () => statusOf("ses_wake").filter((x) => x === "dormant").length === 2);
    // A text event (not a view or a step) is activity too.
    w.emit({ type: "session.text.delta", data: { sessionID: "ses_wake" } });
    await until("idle after a text event", () => lastStatus("ses_wake") === "idle");
    stop();
  });
});

await test("a busy session never goes dormant, and goes dormant after its turn", async () => {
  await withDormantAfter(300, async () => {
    freshHub();
    const sessions = new Map([["ses_busy", session("ses_busy", "/work/b")]]);
    const b = makeContext("/work/b", sessions);
    const stop = await plugin.setup(b.ctx);
    await until("hello", () => registered("ses_busy"));
    b.emit({ type: "session.execution.started", data: { sessionID: "ses_busy" } });
    await until("busy", () => lastStatus("ses_busy") === "busy");
    await new Promise((resolve) => setTimeout(resolve, 900));
    if (lastStatus("ses_busy") !== "busy") throw new Error("a busy session went " + lastStatus("ses_busy"));
    b.emit({ type: "session.execution.succeeded", data: { sessionID: "ses_busy" } });
    await until("idle", () => lastStatus("ses_busy") === "idle");
    await until("dormant after the turn", () => lastStatus("ses_busy") === "dormant");
    stop();
  });
});

await test("a message to a dormant session is delivered and wakes it", async () => {
  await withDormantAfter(300, async () => {
    freshHub();
    const sessions = new Map([["ses_msg", session("ses_msg", "/work/m")]]);
    const m = makeContext("/work/m", sessions);
    const prompts = [];
    m.ctx.session.prompt = async (input) => void prompts.push(input);
    const stop = await plugin.setup(m.ctx);
    await until("dormant", () => lastStatus("ses_msg") === "dormant");
    conns.get("ses_msg").write(JSON.stringify({ method: "deliver", params: { message: {
      message_id: "m_1", conversation_id: "c_1", sender_session_id: "s_x", sender_name: "peer", message_type: "request", body: "wake up" } } }) + "\n");
    await until("prompt", () => prompts.length === 1);
    if (!prompts[0].text.includes("wake up") || prompts[0].sessionID !== "ses_msg") throw new Error("wrong prompt " + JSON.stringify(prompts[0]));
    await until("idle after delivery", () => lastStatus("ses_msg") === "idle");
    stop();
  });
});

await test("a session that reconnects while dormant says idle in hello, then dormant", async () => {
  await withDormantAfter(300, async () => {
    freshHub();
    const sessions = new Map([["ses_re", session("ses_re", "/work/re")]]);
    const r = makeContext("/work/re", sessions);
    const stop = await plugin.setup(r.ctx);
    await until("dormant", () => lastStatus("ses_re") === "dormant");
    const n = hellos.length;
    conns.get("ses_re").destroy(); // the daemon restarts
    await until("second hello", () => hellos.length > n, 4000);
    const h = hellos.at(-1);
    if (h.id !== "ses_re" || h.status !== "idle") throw new Error("hello after reconnect: " + JSON.stringify(h));
    await until("dormant again", () => statuses.filter((x) => x.id === "ses_re" && x.params.status === "dormant").length >= 2);
    stop();
  });
});

await test("the session title becomes the name, and a rename is followed", async () => {
  freshHub();
  const sessions = new Map([
    ["ses_t", session("ses_t", "/Users/n/code/strategies", 0, { title: "Fix the risk model" })],
    ["ses_nt", session("ses_nt", "/Users/n/code/strategies/", 0)],
    ["ses_long", session("ses_long", "/Users/n/code/strategies", 0, { title: "x".repeat(200) })],
  ]);
  const t = makeContext("/Users/n/code/strategies", sessions);
  const stop = await plugin.setup(t.ctx);
  await until("hellos", () => registered("ses_t") && registered("ses_nt") && registered("ses_long"));
  const nameOf = (id) => hellos.find((h) => h.id === id).name;
  if (nameOf("ses_t") !== "strategies: Fix the risk model") throw new Error("name: " + nameOf("ses_t"));
  if (nameOf("ses_nt") !== "strategies") throw new Error("no title: " + nameOf("ses_nt"));
  const long = nameOf("ses_long");
  if (long.length > 60 || !long.startsWith("strategies: xxx") || !long.endsWith("...")) throw new Error("long name: " + long);

  t.emit({ type: "session.renamed", data: { sessionID: "ses_nt", title: "  Add\nbacktest   v2 " } });
  await until("name from rename", () => statuses.some((s) => s.id === "ses_nt" && s.params.name === "strategies: Add backtest v2"));
  // The same title again is not sent twice.
  const n = statuses.length;
  t.emit({ type: "session.renamed", data: { sessionID: "ses_nt", title: "Add backtest v2" } });
  await settle();
  if (statuses.length !== n) throw new Error("an unchanged name was sent again");
  t.emit({ type: "session.renamed", data: { sessionID: "ses_nt", title: "" } });
  await until("name back to the folder", () => statuses.some((s) => s.id === "ses_nt" && s.params.name === "strategies"));
  stop();
});

await test("a sub-agent session does not rename its root session", async () => {
  freshHub();
  const sessions = new Map([
    ["ses_r2", session("ses_r2", "/work/r2", 0, { title: "Root work" })],
    ["ses_k2", session("ses_k2", "/work/r2", 0, { parentID: "ses_r2", title: "Kid work" })],
  ]);
  const r = makeContext("/work/r2", sessions);
  const stop = await plugin.setup(r.ctx);
  await until("hello", () => registered("ses_r2"));
  r.emit({ type: "session.renamed", data: { sessionID: "ses_k2", title: "Kid renamed" } });
  await settle();
  if (statuses.some((s) => s.id === "ses_r2" && s.params.name)) throw new Error("the sub-agent renamed its root");
  stop();
});

await test("a link left by a disposed instance is replaced, not shared", async () => {
  // A reload that did not run the disposer left a link whose owner is gone.
  freshHub();
  let stopped = 0;
  globalThis[Symbol.for("agent-net.opencode.hub.v1")] = {
    links: new Map([["ses_orphan", { link: { stop: () => stopped++ }, owner: Symbol("gone") }]]),
    rootOf: new Map(),
    gone: new Set(),
    instances: new Map(),
  };
  const sessions = new Map([["ses_orphan", session("ses_orphan", "/work/o")]]);
  const o = makeContext("/work/o", sessions);
  const stop = await plugin.setup(o.ctx);
  // Discovery sees the orphan first and replaces it.
  await until("a fresh hello for ses_orphan", () => registered("ses_orphan"));
  if (stopped !== 1) throw new Error("the orphan link was stopped " + stopped + " times");
  // Another live instance does not replace a link it does not own.
  const before = hellos.filter((h) => h.id === "ses_orphan").length;
  const other = makeContext("/work/o", sessions);
  const stopOther = await plugin.setup(other.ctx);
  other.emit({ type: "session.viewed", data: { sessionID: "ses_orphan" } });
  await settle();
  if (hellos.filter((h) => h.id === "ses_orphan").length !== before) throw new Error("a live link was replaced");
  stopOther();
  stop();
});

// OpenCode 2.1.4 and 2.2.0 leave Session.Info.model empty when the session was
// created without an explicit model. The model is then only in the messages.
await test("a session with no model in its info takes it from the last assistant message", async () => {
  freshHub();
  const sessions = new Map([["ses_nomodel", session("ses_nomodel", "/work/nm")]]);
  const context = {
    ses_nomodel: [
      { id: "msg_1", type: "user", time: { created: 1 }, text: "hi" },
      { id: "msg_2", type: "assistant", model: { id: "big-pickle", providerID: "opencode" }, content: [], time: { created: 2 } },
      { id: "msg_3", type: "model-switched", model: { id: "gpt-6", providerID: "openai" }, time: { created: 3 } },
      { id: "msg_4", type: "assistant", model: { id: "gpt-6", providerID: "openai" }, content: [], time: { created: 4 } },
    ],
  };
  const c = makeContext("/work/nm", sessions, { context });
  const stop = await plugin.setup(c.ctx);
  await until("hello for ses_nomodel", () => registered("ses_nomodel"));
  const hello = hellos.find((h) => h.id === "ses_nomodel");
  if (hello.model !== "openai/gpt-6") throw new Error("hello model: " + hello.model);
  stop();
});

await test("a session with a model in its info does not read its messages", async () => {
  freshHub();
  const sessions = new Map([["ses_hasmodel", session("ses_hasmodel", "/work/hm", 0, { model: { id: "x", providerID: "p" } })]]);
  let reads = 0;
  const c = makeContext("/work/hm", sessions, { onContext: () => reads++ });
  const stop = await plugin.setup(c.ctx);
  await until("hello for ses_hasmodel", () => registered("ses_hasmodel"));
  if (reads !== 0) throw new Error("read the messages of a session that has a model");
  stop();
});

await test("a session with no model and unreadable messages registers with no model", async () => {
  freshHub();
  const sessions = new Map([["ses_broken", session("ses_broken", "/work/br")]]);
  const c = makeContext("/work/br", sessions, { contextFails: true });
  const stop = await plugin.setup(c.ctx);
  await until("hello for ses_broken", () => registered("ses_broken"));
  if (hellos.find((h) => h.id === "ses_broken").model !== undefined) throw new Error("invented a model");
  stop();
});

function pushText(sessionID, params) {
  conns.get(sessionID).write(JSON.stringify({ method: "session_text", params }) + "\n");
}

await test("session_text answers with the conversation: prompts, replies, tool calls and results", async () => {
  freshHub();
  const sessions = new Map([["ses_txt", session("ses_txt", "/work/txt")]]);
  const context = {
    ses_txt: [
      { id: "msg_a", type: "user", time: { created: 1000 }, text: "fix the build", files: [] },
      { id: "msg_b", type: "agent-switched", time: { created: 1001 } },
      {
        id: "msg_c", type: "assistant", time: { created: 1002 }, model: { id: "m", providerID: "p" },
        content: [
          { type: "reasoning", text: "thinking out loud" },
          { type: "text", text: "Looking." },
          { type: "tool", id: "t1", name: "bash", state: { status: "completed", input: { command: "go build ./..." }, content: [{ type: "text", text: "ok" }] } },
          { type: "tool", id: "t2", name: "read", state: { status: "error", input: { filePath: "/x" }, error: { type: "x", message: "no such file" } } },
        ],
      },
      { id: "msg_d", type: "shell", time: { created: 1003 }, command: "ls", output: { output: "a b c" } },
      { id: "msg_e", type: "idle", time: { created: 1004 } },
      { id: "msg_f", type: "user", time: { created: 1005 }, text: "x".repeat(5000), files: [{}, {}] },
    ],
  };
  const c = makeContext("/work/txt", sessions, { context });
  const stop = await plugin.setup(c.ctx);
  await until("hello for ses_t", () => registered("ses_txt"));
  pushText("ses_txt", { req: "r1", limit: 50 });
  await until("the answer", () => textResults.some((r) => r.req === "r1"));
  const r = textResults.find((x) => x.req === "r1");
  if (!r.ok) throw new Error("not ok: " + r.error);
  const got = r.page.entries.map((e) => `${e.role}|${e.tool ?? ""}|${e.text.slice(0, 40)}`);
  const want = [
    "user||fix the build",
    "assistant||Looking.",
    "tool_call|bash|bash: go build ./...",
    "tool_result|bash|ok",
    "tool_call|read|read: /x",
    "tool_result|read|error: no such file",
    "tool_call|shell|shell: ls",
    "tool_result|shell|a b c",
  ];
  const long = r.page.entries[r.page.entries.length - 1];
  if (JSON.stringify(got.slice(0, 8)) !== JSON.stringify(want)) throw new Error("entries: " + JSON.stringify(got));
  if (long.role !== "user" || !long.cut || Array.from(long.text).length !== 2000 || !long.text.endsWith("x")) {
    throw new Error("the long prompt was not cut to 2000 characters: " + long.text.length);
  }
  if (r.page.entries[0].time !== 1000) throw new Error("time: " + r.page.entries[0].time);
  if (r.page.older !== undefined) throw new Error("older should be empty when the start is reached");
  if (!r.page.newer) throw new Error("no cursor to follow from");
  stop();
});

await test("session_text pages back and forward by cursor, and reports failures", async () => {
  freshHub();
  const sessions = new Map([["ses_pg", session("ses_pg", "/work/pg")]]);
  const msgs = [];
  for (let i = 0; i < 10; i++) msgs.push({ id: "msg_" + i, type: "user", time: { created: i }, text: "prompt " + i, files: [] });
  const hooks = { context: { ses_pg: msgs } };
  const c = makeContext("/work/pg", sessions, hooks);
  const stop = await plugin.setup(c.ctx);
  await until("hello for ses_pg", () => registered("ses_pg"));
  const ask = async (id, params) => {
    pushText("ses_pg", { req: id, ...params });
    await until("answer " + id, () => textResults.some((r) => r.req === id));
    return textResults.find((r) => r.req === id);
  };
  const newest = await ask("p1", { limit: 3 });
  const texts = (r) => r.page.entries.map((e) => e.text).join(",");
  if (texts(newest) !== "prompt 7,prompt 8,prompt 9" || !newest.page.older) throw new Error("newest: " + texts(newest));
  const older = await ask("p2", { limit: 3, before: newest.page.older });
  if (texts(older) !== "prompt 4,prompt 5,prompt 6") throw new Error("older: " + texts(older));
  const oldest = await ask("p3", { limit: 5, before: older.page.older });
  if (texts(oldest) !== "prompt 0,prompt 1,prompt 2,prompt 3" || oldest.page.older !== undefined) throw new Error("oldest: " + texts(oldest));
  msgs.push({ id: "msg_10", type: "user", time: { created: 10 }, text: "prompt 10", files: [] });
  const news = await ask("p4", { after: newest.page.newer });
  if (texts(news) !== "prompt 10") throw new Error("after: " + texts(news));
  const none = await ask("p5", { after: news.page.newer });
  if (none.page.entries.length !== 0 || none.page.newer !== news.page.newer) throw new Error("nothing new: " + JSON.stringify(none.page));
  const gone = await ask("p6", { before: "msg_missing:0" });
  if (gone.page.entries.length !== 0) throw new Error("an unknown cursor should give nothing");
  hooks.contextFails = true;
  const failed = await ask("p7", {});
  if (failed.ok || !failed.error.includes("context failed")) throw new Error("failure: " + JSON.stringify(failed));
await test("agent_start and agent_stop reach the daemon and print a short result", async () => {
  freshHub();
  const sessions = new Map([["ses_w", session("ses_w", "/work/w")]]);
  let tools;
  const c = makeContext("/work/w", sessions, {
    onTransform(fn) {
      tools = [];
      fn({ add: (t) => tools.push(t) });
    },
  });
  const stop = await plugin.setup(c.ctx);
  await until("hello for ses_w", () => registered("ses_w"));
  const start = tools.find((t) => t.name === "agent_start");
  const stopTool = tools.find((t) => t.name === "agent_stop");
  if (!start || !stopTool) throw new Error("worker tools are not registered");
  // The caller is the bound session: there is no way to name another one.
  for (const bad of ["owner", "from", "sender", "session_id"]) {
    if (bad in start.input.properties) throw new Error("agent_start exposes " + bad);
  }
  const before = calls.length;
  const out = (await start.execute({ project: "alpha", task: "say ok", model: "a/x" }, { sessionID: "ses_w" })).content;
  const [method, params] = calls[before];
  if (method !== "worker_start" || params.project !== "alpha" || params.task !== "say ok" || params.model !== "a/x") {
    throw new Error("wrong call: " + JSON.stringify(calls[before]));
  }
  for (const want of ["Started worker s_worker", "project alpha on mac", "agent_stop"]) {
    if (!out.includes(want)) throw new Error("missing " + JSON.stringify(want) + " in " + JSON.stringify(out));
  }
  const gone = (await stopTool.execute({ session: "s_worker" }, { sessionID: "ses_w" })).content;
  const last = calls[calls.length - 1];
  if (last[0] !== "worker_stop" || last[1].session !== "s_worker" || !gone.includes("Stopped worker s_worker")) {
    throw new Error("stop: " + JSON.stringify(last) + " " + gone);
  }
  stop();
});

await test("a message from the owner says it is from the user in person", async () => {
  freshHub();
  const sessions = new Map([["ses_o", session("ses_o", "/work/o")]]);
  const prompts = [];
  const c = makeContext("/work/o", sessions, { onPrompt: (p) => prompts.push(p) });
  const stop = await plugin.setup(c.ctx);
  await until("hello for ses_o", () => registered("ses_o"));
  const push = (m) => conns.get("ses_o").write(JSON.stringify({ method: "deliver", params: { message: m } }) + "\n");
  push({ message_id: "m_1", sender_session_id: "s_me", sender_name: "you (on mac)", sender_harness: "owner",
         message_type: "request", body: "please also run the tests" });
  await until("prompt for the owner message", () => prompts.length === 1);
  const text = prompts[0].text;
  for (const want of ["please also run the tests", "from your user in person (you (on mac)", "This is your user speaking, not another agent", 'message_id="m_1"']) {
    if (!text.includes(want)) throw new Error("missing " + JSON.stringify(want) + " in " + JSON.stringify(text));
  }
  if (text.includes("another agent working on your user's machines")) throw new Error("an owner message is labelled as an agent's");
  // An agent's message keeps its own text.
  push({ message_id: "m_2", sender_session_id: "s_a", sender_name: "alice", sender_harness: "pi", message_type: "request", body: "hello" });
  await until("prompt for the agent message", () => prompts.length === 2);
  if (!prompts[1].text.includes("another agent working on your user's machines")) throw new Error("agent text: " + prompts[1].text);
  stop();
});

await test("a worker does not register the user's other sessions", async () => {
  freshHub();
  const sessions = new Map([
    ["ses_other", session("ses_other", "/work/x")],
    ["ses_older", session("ses_older", "/work/x")],
    ["ses_mine", session("ses_mine", "/work/x")],
  ]);
  const file = join(dir, "worker-session");
  process.env.AGENT_NET_WORKER = "w_1";
  process.env.AGENT_NET_WORKER_SESSION_FILE = file;
  try {
    let listed = 0;
    const c = makeContext("/work/x", sessions, { onList: () => listed++ });
    const stop = await plugin.setup(c.ctx);
    await settle();
    if (listed !== 0 || registered("ses_other") || registered("ses_older") || registered("ses_mine")) {
      throw new Error("a worker ran discovery");
    }
    // The daemon names the worker's session in a file; the plugin registers that one only.
    writeFileSync(file, "ses_mine\n");
    await until("hello for the worker's own session", () => registered("ses_mine"));
    await settle();
    if (registered("ses_other") || registered("ses_older")) throw new Error("a worker registered a session that is not its own");
    stop();
  } finally {
    delete process.env.AGENT_NET_WORKER;
    delete process.env.AGENT_NET_WORKER_SESSION_FILE;
  }
});

await test("agent_handback reaches the daemon; agent_start can ask for a worktree", async () => {
  freshHub();
  const sessions = new Map([["ses_h", session("ses_h", "/work/h")]]);
  let tools;
  const c = makeContext("/work/h", sessions, {
    onTransform(fn) {
      tools = [];
      fn({ add: (t) => tools.push(t) });
    },
  });
  const stop = await plugin.setup(c.ctx);
  await until("hello for ses_h", () => registered("ses_h"));
  const hb = tools.find((t) => t.name === "agent_handback");
  if (!hb) throw new Error("agent_handback is not registered");
  if (!hb.input.required.includes("report")) throw new Error("report must be required");
  for (const bad of ["to", "owner", "session", "recipient"]) {
    if (bad in hb.input.properties) throw new Error("agent_handback lets the worker pick a recipient: " + bad);
  }
  const before = calls.length;
  const out = (await hb.execute({ report: "all done", status: "blocked" }, { sessionID: "ses_h" })).content;
  const [method, params] = calls[before];
  if (method !== "worker_handback" || params.report !== "all done" || params.status !== "blocked" || !out.includes("Handed back (blocked)")) {
    throw new Error("handback: " + JSON.stringify(calls[before]) + " " + out);
  }
  const start = tools.find((t) => t.name === "agent_start");
  if (!("isolation" in start.input.properties)) throw new Error("agent_start has no isolation");
  await start.execute({ project: "alpha", task: "t", isolation: "worktree" }, { sessionID: "ses_h" });
  if (calls[calls.length - 1][1].isolation !== "worktree") throw new Error("isolation was not passed");
  stop();
});

await test("a report from a worker is framed as model output, not the user's words", async () => {
  freshHub();
  const sessions = new Map([["ses_r", session("ses_r", "/work/r2")]]);
  const prompts = [];
  const c = makeContext("/work/r2", sessions, { onPrompt: (p) => prompts.push(p) });
  const stop = await plugin.setup(c.ctx);
  await until("hello for ses_r", () => registered("ses_r"));
  const push = (m) => conns.get("ses_r").write(JSON.stringify({ method: "deliver", params: { message: m } }) + "\n");
  push({ message_id: "m_w", sender_session_id: "s_w", sender_name: "shop", sender_harness: "opencode", sender_worker: true,
         message_type: "reply", body: "[agent-net worker notice]\nstatus: done" });
  await until("prompt for the worker report", () => prompts.length === 1);
  for (const want of ["status: done", "model output from a worker", "not your user's words", "no user authority"]) {
    if (!prompts[0].text.includes(want)) throw new Error("missing " + JSON.stringify(want) + " in " + JSON.stringify(prompts[0].text));
  }
  // The owner's own message is never framed that way, and a plain agent's is not either.
  push({ message_id: "m_p", sender_session_id: "s_p", sender_name: "pal", sender_harness: "pi", message_type: "request", body: "hi" });
  await until("prompt for the agent message", () => prompts.length === 2);
  if (prompts[1].text.includes("model output from a worker")) throw new Error("an agent message got the worker frame");
  stop();
});

server.close();
rmSync(dir, { recursive: true, force: true });
if (failures) {
  console.log(failures + " test(s) failed");
  process.exit(1);
}
console.log("all tests passed");
process.exit(0);
