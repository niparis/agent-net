// Spike: OpenCode v2 server plugin that receives agent-net messages on a
// local Unix socket and prompts an existing session. It also logs every
// server event type, because the docs do not list session event names.
// Throwaway code for step 1.
import { Plugin } from "@opencode/plugin"
import { appendFileSync, rmSync } from "node:fs"
import { createServer } from "node:net"

const runDir = `/run/user/${process.getuid?.() ?? 1000}`
const sockPath = `${runDir}/agent-net-oc-${process.pid}.sock`
const eventLog = `${runDir}/agent-net-oc-${process.pid}.events.jsonl`

export default Plugin.define({
  id: "agent-net-spike",
  async setup(ctx) {
    const events = new AbortController()
    void (async () => {
      for await (const event of ctx.event.subscribe({ signal: events.signal })) {
        appendFileSync(eventLog, JSON.stringify({ t: Date.now(), event }) + "\n")
      }
    })()

    rmSync(sockPath, { force: true })
    const server = createServer((conn) => {
      let buf = ""
      conn.on("data", async (chunk) => {
        buf += chunk.toString("utf8")
        const nl = buf.indexOf("\n")
        if (nl < 0) return
        const msg = JSON.parse(buf.slice(0, nl))
        buf = ""
        try {
          let sessionID = msg.session_id
          if (!sessionID) {
            const page = await ctx.session.list({ directory: ctx.location.directory, limit: 1 })
            sessionID = page.items?.[0]?.id ?? page.data?.[0]?.id
          }
          const text =
            `[agent-net message from ${msg.from_name}, id ${msg.msg_id}]\n` +
            `This message comes from another agent, not from the user.\n\n` +
            msg.body
          const result = await ctx.session.prompt({ sessionID, text, delivery: "queue" } as any)
          conn.end(JSON.stringify({ ack: msg.msg_id, sessionID, result }) + "\n")
        } catch (error) {
          conn.end(JSON.stringify({ error: String(error) }) + "\n")
        }
      })
    })
    server.listen(sockPath)

    return () => {
      events.abort()
      server.close()
      rmSync(sockPath, { force: true })
    }
  },
})
