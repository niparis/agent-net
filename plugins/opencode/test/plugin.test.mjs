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
      conn.write(JSON.stringify({ id: line.id, result: {} }) + "\n");
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
      tool: { transform() {} },
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

server.close();
rmSync(dir, { recursive: true, force: true });
if (failures) {
  console.log(failures + " test(s) failed");
  process.exit(1);
}
console.log("all tests passed");
process.exit(0);
