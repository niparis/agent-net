// Fake agent-net daemon for testing the Pi extension. No dependencies.
// Usage: node fake-daemon.mjs <socket path> [--deliver-after-ms N]
// Logs every received line to stdout. Answers each method with a canned result,
// and pushes one `deliver` after `hello` (default 2000 ms).
import { createServer } from "node:net";
import { rmSync } from "node:fs";

const sockPath = process.argv[2];
if (!sockPath) {
  console.error("usage: fake-daemon.mjs <socket path> [--deliver-after-ms N]");
  process.exit(2);
}
const i = process.argv.indexOf("--deliver-after-ms");
const deliverAfter = i > 0 ? Number(process.argv[i + 1]) : 2000;

const canned = {
  hello: { session_id: "s_00000000000000aa" },
  status: {},
  announce: { ok: true },
  list: { agents: [{ session_id: "s_00000000000000bb", name: "peer", harness: "claude", online: true }] },
  get: { session_id: "s_00000000000000bb", name: "peer", harness: "claude", online: true },
  send: { message_id: "m_0000000000000001", state: "stored" },
  reply: { message_id: "m_0000000000000002", state: "stored" },
  messages: { messages: [] },
  message_status: { message_id: "m_0000000000000001", state: "delivered" },
  delivered: {},
};

rmSync(sockPath, { force: true });
const server = createServer((conn) => {
  console.log("CONNECT");
  let buf = "";
  conn.on("data", (chunk) => {
    buf += chunk.toString("utf8");
    let nl;
    while ((nl = buf.indexOf("\n")) >= 0) {
      const line = buf.slice(0, nl);
      buf = buf.slice(nl + 1);
      if (!line.trim()) continue;
      console.log("RECV " + line);
      const req = JSON.parse(line);
      const result = canned[req.method];
      const resp = result ? { id: req.id, result } : { id: req.id, error: "unknown method " + req.method };
      conn.write(JSON.stringify(resp) + "\n");
      if (req.method === "hello") {
        setTimeout(() => {
          const push = {
            method: "deliver",
            params: {
              message: {
                message_id: "m_00000000000000cc",
                conversation_id: "c_00000000000000dd",
                sender_session_id: "s_00000000000000bb",
                sender_name: "peer",
                recipient_session_id: "s_00000000000000aa",
                message_type: "request",
                body: "What is 6 times 7? Answer with just the number.",
                hop: 0,
              },
            },
          };
          console.log("PUSH deliver");
          conn.write(JSON.stringify(push) + "\n");
        }, deliverAfter);
      }
    }
  });
  conn.on("close", () => console.log("DISCONNECT"));
  conn.on("error", () => {});
});
server.listen(sockPath, () => console.log("LISTENING " + sockPath));
process.on("SIGTERM", () => {
  server.close();
  rmSync(sockPath, { force: true });
  process.exit(0);
});
