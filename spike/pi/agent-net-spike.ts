// Spike: Pi extension that receives agent-net messages on a local Unix
// socket and injects them into the running session. Throwaway code for step 1.
//
// Load with: pi -e ./spike/pi/agent-net-spike.ts
// Wire format (one JSON line per connection):
//   {"msg_id":"...","from_name":"...","reply_to":"/run/user/1000/x.sock","body":"..."}
import { type ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import { createConnection, createServer, type Server } from "node:net";
import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

const runDir = `/run/user/${process.getuid?.() ?? 1000}`;
const sockPath = join(runDir, `agent-net-pi-${process.pid}.sock`);
const stateDir = join(homedir(), ".pi", "agent-net-spike");
const statePath = join(stateDir, `${process.pid}.json`);

export default function (pi: ExtensionAPI) {
  let server: Server | undefined;
  let state: Record<string, unknown> = { pid: process.pid, socket: sockPath, status: "idle" };

  const writeState = (patch: Record<string, unknown>) => {
    state = { ...state, ...patch, updatedAt: Date.now() };
    mkdirSync(stateDir, { recursive: true });
    writeFileSync(statePath, JSON.stringify(state));
  };

  pi.on("session_start", async (event, ctx) => {
    writeState({
      sessionId: ctx.sessionManager.getSessionId?.(),
      sessionFile: ctx.sessionManager.getSessionFile?.() ?? "ephemeral",
      cwd: ctx.cwd,
      reason: event.reason,
    });
    rmSync(sockPath, { force: true });
    server = createServer((conn) => {
      let buf = "";
      conn.on("data", (chunk) => {
        buf += chunk.toString("utf8");
        let nl: number;
        while ((nl = buf.indexOf("\n")) >= 0) {
          const line = buf.slice(0, nl);
          buf = buf.slice(nl + 1);
          try {
            const msg = JSON.parse(line);
            const idle = ctx.isIdle();
            pi.sendMessage(
              {
                customType: "agent-net",
                content:
                  `[agent-net message from ${msg.from_name}, id ${msg.msg_id}]\n` +
                  `This message comes from another agent, not from the user.\n` +
                  `Reply with the agent_net_reply tool, passing reply_to="${msg.reply_to}".\n\n` +
                  msg.body,
                display: true,
                details: { from: msg.from_name, msgId: msg.msg_id, replyTo: msg.reply_to },
              },
              { deliverAs: "followUp", triggerTurn: true },
            );
            conn.write(JSON.stringify({ ack: msg.msg_id, idle }) + "\n");
          } catch (error) {
            conn.write(JSON.stringify({ error: String(error) }) + "\n");
          }
        }
      });
    });
    server.listen(sockPath);
  });

  pi.on("agent_start", async () => writeState({ status: "busy" }));
  pi.on("agent_settled", async () => writeState({ status: "idle" }));

  pi.on("session_shutdown", async () => {
    server?.close();
    rmSync(sockPath, { force: true });
    rmSync(statePath, { force: true });
  });

  pi.registerTool({
    name: "agent_net_reply",
    label: "Reply to agent",
    description: "Reply to a message that came from another agent through agent-net.",
    parameters: Type.Object({
      reply_to: Type.String({ description: "The reply_to address from the incoming message" }),
      text: Type.String({ description: "Reply text" }),
    }),
    async execute(_toolCallId, params) {
      await new Promise<void>((resolve, reject) => {
        const c = createConnection(params.reply_to, () => {
          c.end(JSON.stringify({ from_pid: process.pid, text: params.text }) + "\n", () => resolve());
        });
        c.on("error", reject);
      });
      return { content: [{ type: "text", text: "Reply sent." }], details: {} };
    },
  });
}
