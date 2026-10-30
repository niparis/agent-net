import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { AgentNetWorker } from '../types'

const workersAtom = atom({ plugin: 'agent-net-workers', key: 'workers' } as const, [] as AgentNetWorker[])
const notifiedAtom = atom({ plugin: 'agent-net-workers', key: 'notified' } as const, [] as string[])

const POLL_MS = 3000
const RUN_TIMEOUT_MS = 2000
const NOTIFIED_MAX = 200
const ENDED_MARK: Record<string, string> = { done: '✓', failed: '✗', blocked: '✗', 'timed out': '✗', stopped: '✗' }

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

function shortId(id: string): string {
  return id.slice(-4)
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

function clip(text: string, columns: number): string {
  if (columns <= 0) return ''
  const chars = Array.from(text)
  if (chars.length <= columns) return text
  if (columns <= 3) return chars.slice(0, columns).join('')
  return chars.slice(0, columns - 3).join('') + '...'
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
        {workers.map(w => {
          const isEnded = w.endStatus !== ''
          const mark = isEnded ? (ENDED_MARK[w.endStatus] ?? '✗') : '○'
          const color = isEnded ? (w.endStatus === 'done' ? 'success' : 'error') : undefined
          const state = isEnded ? w.endStatus : (w.status || 'running')
          const elapsed = (isEnded ? w.endedAt : now) - w.startedAt
          const head = [shortId(w.sessionId), w.harness + (w.model ? ' · ' + modelName(w.model) : ''),
            state, shortDuration(elapsed * 1000)].filter(Boolean).join(' ')
          const text = (isEnded ? (w.endNote || w.task) : w.task).split('\n')[0]
          // Keep each worker on one row: leave room for the mark, the spaces and
          // anything the surface draws at the right edge.
          const rest = clip(text, Math.max(0, columns - 8 - head.length))
          return (
            <Box key={w.sessionId}>
              <Text wrap="truncate-end">
                <Text color={color}>{mark} </Text>
                <Text dimColor>{head} </Text>
                <Text>{rest}</Text>
              </Text>
            </Box>
          )
        })}
      </Box>
    )
  })
}
