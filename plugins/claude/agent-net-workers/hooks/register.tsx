import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { AgentNetWorker } from '../types'

const workersAtom = atom({ plugin: 'agent-net-workers', key: 'workers' } as const, [] as AgentNetWorker[])
const notifiedAtom = atom({ plugin: 'agent-net-workers', key: 'notified' } as const, [] as string[])

const POLL_MS = 3000
const RUN_TIMEOUT_MS = 2000
const NOTIFIED_MAX = 200

// RawWorker is the JSON `agent-net workers` writes, before it becomes a value
// the band and the status line read.
type RawWorker = {
  session_id?: string
  harness?: string
  model?: string
  project?: string
  status?: string
  online?: boolean
  started_at?: number
  task?: string
  context_tokens?: number
  context_limit?: number
  tokens?: number
  ended_at?: number
  end_status?: string
  end_note?: string
}

function workerOf(raw: RawWorker): AgentNetWorker | null {
  if (!raw.session_id) return null
  return {
    sessionId: raw.session_id,
    harness: raw.harness ?? '',
    model: raw.model ?? '',
    project: raw.project ?? '',
    status: raw.status ?? '',
    online: raw.online === true,
    startedAt: raw.started_at ?? 0,
    task: raw.task ?? '',
    contextTokens: raw.context_tokens ?? 0,
    contextLimit: raw.context_limit ?? 0,
    tokens: raw.tokens ?? 0,
    endedAt: raw.ended_at ?? 0,
    endStatus: raw.end_status ?? '',
    endNote: raw.end_note ?? '',
  }
}

// isVisible drops a worker the gateway still lists as offline long after it
// ended. The recent end facts are the only sign a stopped worker should show.
function isVisible(w: AgentNetWorker): boolean {
  return w.online || w.endStatus !== ''
}

// modelName drops the provider prefix: "anthropic/claude-sonnet-5" reads
// "claude-sonnet-5".
function modelName(model: string): string {
  const slash = model.lastIndexOf('/')
  return slash >= 0 ? model.slice(slash + 1) : model
}

// shortDuration writes a run time the way a task list does: 42s, 2m, 1h4m.
function shortDuration(ms: number): string {
  const seconds = Math.max(0, Math.round(ms / 1000))
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m`
  return `${Math.floor(minutes / 60)}h${minutes % 60}m`
}

// contextOf writes the context window line of a worker, and the color of the
// percentage: warning at 70 percent and over, error at 90 percent and over.
// It is undefined when the harness has not reported either figure.
function contextOf(w: AgentNetWorker): { text: string; color?: 'warning' | 'error' } | undefined {
  if (!(w.contextTokens > 0) || !(w.contextLimit > 0)) return undefined
  const percent = Math.round((w.contextTokens / w.contextLimit) * 100)
  const color = percent >= 90 ? 'error' : percent >= 70 ? 'warning' : undefined
  return { text: `ctx ${percent}%`, color }
}

function clip(text: string, columns: number): string {
  if (columns <= 0) return ''
  const chars = Array.from(text)
  if (chars.length <= columns) return text
  return chars.slice(0, columns - 1).join('') + '…'
}

// Match the TUI's shortWorkerModel: drop provider and claude- prefixes,
// then the effort suffix or a whole trailing segment before clipping a word.
function shortWorkerModel(model: string, columns: number): string {
  const name = modelName(model).replace(/^claude-/, '')
  if (Array.from(name).length <= columns) return name
  const colon = name.lastIndexOf(':')
  if (colon > 0 && colon <= columns) return name.slice(0, colon)
  for (let i = name.length - 1; i > 0; i--) {
    if ('-._:/'.includes(name[i]!) && i + 1 <= columns) return name.slice(0, i) + '…'
  }
  return clip(name, columns)
}

// Compute columns once for the visible pane. Empty cells keep their space,
// while columns with no values take none. Status replaces the old circle:
// busy is implicit, other states have a colored label.
export function formatWorkerRows(workers: AgentNetWorker[], now: number, columns: number) {
  const rows = workers.map(w => {
    const ended = w.endStatus !== ''
    const state = ended ? w.endStatus : w.status === 'busy' ? '' : w.status
    const ctx = contextOf(w)
    return {
      fields: [w.harness, shortWorkerModel(w.model, 24), state,
        shortDuration(((ended ? w.endedAt : now) - w.startedAt) * 1000), ctx?.text ?? ''],
      task: (ended ? w.endNote || w.task : w.task).split('\n')[0] ?? '',
      statusColor: state ? (ended ? (state === 'done' ? 'success' : 'error') : 'warning') : undefined,
      contextColor: ctx?.color,
    } as const
  })
  const widths = [0, 1, 2, 3, 4].map(i => Math.max(0, ...rows.map(r => Array.from(r.fields[i]!).length)))
  const prefixWidth = () => widths.reduce((n, w) => n + (w > 0 ? w + 2 : 0), 0)
  // Leave task room in a narrow pane; the model is the first field to shrink.
  const taskRoom = Math.min(16, Math.floor(columns / 3))
  for (const i of [1, 0, 2, 4, 3]) {
    widths[i] = Math.max(0, widths[i]! - Math.max(0, prefixWidth() + taskRoom - columns))
  }
  return rows.map(r => ({
    ...r,
    fields: r.fields.map((value, i) => {
      const width = widths[i]!
      if (width === 0) return ''
      const text = i === 1 ? shortWorkerModel(value, width) : clip(value, width)
      return text + ' '.repeat(width - Array.from(text).length + 2)
    }),
    task: clip(r.task, Math.max(0, columns - prefixWidth())),
  }))
}

// noticeDuration writes a run time with both parts, as a finished notice does:
// 3m 7s, 42s, 1h 4m.
function noticeDuration(ms: number): string {
  const seconds = Math.max(0, Math.round(ms / 1000))
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (hours > 0) return `${hours}h ${minutes}m`
  if (minutes > 0) return `${minutes}m ${seconds % 60}s`
  return `${seconds}s`
}

// endNotice is the one line appended to the transcript when a worker ends.
function endNotice(w: AgentNetWorker): string {
  const who = [w.harness, modelName(w.model)].filter(Boolean).join(' · ')
  const run = noticeDuration((w.endedAt - w.startedAt) * 1000)
  const what = w.endNote || w.task
  return `agent-net worker ${who} ${w.endStatus} · ${run}` + (what ? `: ${what}` : '')
}

// notifyEnds appends one notice per ended worker, and remembers the worker in
// $.state so a reload does not append it again. A refused append is ignored.
async function notifyEnds($: EngineInterface, rows: AgentNetWorker[]): Promise<void> {
  const notified = new Set(await read($, notifiedAtom))
  let changed = false
  for (const w of rows) {
    if (w.endStatus === '' || notified.has(w.sessionId)) continue
    notified.add(w.sessionId)
    changed = true
    try {
      await $.session.append({
        message: { type: 'system', content: [{ type: 'text', text: endNotice(w) }] },
      })
    } catch {
      // Refused or unavailable: the worker still shows in the band.
    }
  }
  if (changed) {
    await update($, notifiedAtom, () => Array.from(notified).slice(-NOTIFIED_MAX))
  }
}

function statusText(rows: AgentNetWorker[]): string | undefined {
  if (rows.length === 0) return undefined
  const ended = new Map<string, number>()
  let busy = 0
  let idle = 0
  for (const w of rows) {
    if (w.endStatus !== '') {
      ended.set(w.endStatus, (ended.get(w.endStatus) ?? 0) + 1)
    } else if (w.status === 'busy' || w.status === 'shell') {
      busy += 1
    } else {
      idle += 1
    }
  }
  const parts: string[] = []
  if (busy > 0) parts.push(`${busy} busy`)
  if (idle > 0) parts.push(`${idle} idle`)
  for (const status of ['done', 'blocked', 'failed', 'timed out', 'stopped']) {
    const n = ended.get(status) ?? 0
    if (n > 0) parts.push(`${n} ${status}`)
  }
  return `agent-net: ${parts.join(', ')}`
}

async function clear($: EngineInterface): Promise<void> {
  await update($, workersAtom, () => [])
  $.ui.status(undefined)
}

async function poll($: EngineInterface, sessionId: string): Promise<void> {
  try {
    const { exitCode, stdout } = await $.process.run(
      ['agent-net', 'workers', '-json', '-recent', '-owner-claude', sessionId],
      { timeoutMs: RUN_TIMEOUT_MS },
    )
    if (exitCode !== 0) {
      await clear($)
      return
    }
    const parsed = JSON.parse(stdout) as { workers?: RawWorker[] }
    const rows: AgentNetWorker[] = []
    for (const raw of parsed.workers ?? []) {
      const worker = workerOf(raw)
      if (worker && isVisible(worker)) rows.push(worker)
    }
    await update($, workersAtom, () => rows)
    $.ui.status(statusText(rows))
    await notifyEnds($, rows)
  } catch {
    // agent-net is not installed, or it failed: show nothing, and no error.
    await clear($)
  }
}

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    const result = await next(e)
    const sessionId = await $.session.id()
    const tick = () => {
      void poll($, sessionId)
    }
    tick()
    $.clock.every(POLL_MS, tick)
    return result
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    const workers = (await read($, workersAtom)).filter(isVisible)
    if (workers.length === 0 || e.props.hasSurvey) {
      return next(e)
    }

    const now = Math.floor((await $.clock.now()) / 1000)
    const columns = e.props.bodyColumns
    const { Box, Text } = $.ui.resolve(e)

    const label = ' agent-net workers '
    const rule = '─'.repeat(2) + label + '─'.repeat(Math.max(0, columns - 2 - label.length))
    return (
      <Box flexDirection="column">
        <Text dimColor wrap="truncate-end">{rule}</Text>
        {formatWorkerRows(workers, now, columns).map((row, i) => (
          <Box key={workers[i]!.sessionId}>
            <Text wrap="truncate-end">
              <Text dimColor>{row.fields[0]}{row.fields[1]}</Text>
              <Text color={row.statusColor}>{row.fields[2]}</Text>
              <Text dimColor>{row.fields[3]}</Text>
              <Text color={row.contextColor}>{row.fields[4]}</Text>
              <Text>{row.task}</Text>
            </Text>
          </Box>
        ))}
      </Box>
    )
  })
}
