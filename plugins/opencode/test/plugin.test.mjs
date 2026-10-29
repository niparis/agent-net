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
const closed = [];
const calls = []; // [method, params] of every call except hello
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
        hellos.push({ id: line.params.native_id, cwd: line.params.cwd });
      }
      if (line.method !== "hello") calls.push([line.method, line.params]);
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
        async prompt() {},
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
  const sessions = new Map([["ses_other_dir", session("ses_other_dir", "/work/b", 3 * 60 * 60 * 1000)]]);
  const a = makeContext("/work/a", sessions);
  const stop = await plugin.setup(a.ctx);
  await until("hello for ses_other_dir", () => registered("ses_other_dir"));
  stop();
});

await test("startup discovery skips old, archived and sub-agent sessions", async () => {
  freshHub();
  const sessions = new Map([
    ["ses_old", session("ses_old", "/work/a", 3 * 24 * 60 * 60 * 1000)],
    ["ses_arch", session("ses_arch", "/work/a", 0, { time: { updated: Date.now(), archived: Date.now() } })],
    ["ses_child", session("ses_child", "/work/a", 0, { parentID: "ses_new2" })],
    ["ses_new2", session("ses_new2", "/work/a")],
  ]);
  const a = makeContext("/work/a", sessions);
  const stop = await plugin.setup(a.ctx);
  await until("hello for ses_new2", () => registered("ses_new2"));
  await settle();
  for (const id of ["ses_old", "ses_arch", "ses_child"]) {
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

server.close();
rmSync(dir, { recursive: true, force: true });
if (failures) {
  console.log(failures + " test(s) failed");
  process.exit(1);
}
console.log("all tests passed");
process.exit(0);
