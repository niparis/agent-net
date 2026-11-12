import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { AgentNetWorker } from '../types'

const workersAtom = atom({ plugin: 'agent-net-workers', key: 'workers' } as const, [] as AgentNetWorker[])
const notifiedAtom = atom({ plugin: 'agent-net-workers', key: 'notified' } as const, [] as string[])
const staleSecondsAtom = atom({ plugin: 'agent-net-workers', key: 'stale-seconds' } as const, 0)

const BINARY_PATHS = '__AGENT_NET_BINARY_PATHS__'

export function binaryPaths(value = BINARY_PATHS): string[] {
  if (value === '__AGENT_NET_BINARY_' + 'PATHS__') return ['agent-net']
  try { return [...new Set([...JSON.parse(value) as string[], 'agent-net'])] } catch { return ['agent-net'] }
}

const PANEL_VERSION = '__AGENT_NET_PANEL_VERSION__'
const outdatedAtom = atom({ plugin: 'agent-net-workers', key: 'outdated' } as const, false)

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
  title?: string
  context_tokens?: number
  context_limit?: number
  tokens?: number
  ended_at?: number
  end_status?: string
  end_note?: string
  check?: string
  difficulty_used?: string
  assessment?: { d?: number; label?: string; picker_label?: string; error?: string }
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
    title: raw.title ?? '',
    contextTokens: raw.context_tokens ?? 0,
    contextLimit: raw.context_limit ?? 0,
    tokens: raw.tokens ?? 0,
    endedAt: raw.ended_at ?? 0,
    endStatus: raw.end_status ?? '',
    endNote: raw.end_note ?? '',
    check: raw.check ?? '',
    difficulty: difficultyOf(raw),
  }
}

// difficultyOf is the level the picker used, with the judge's D when the
// judge's label was the one used: "normal 2.60", or "normal" alone.
function difficultyOf(raw: RawWorker): string {
  const a = raw.assessment
  const level = a?.picker_label || raw.difficulty_used || ''
  if (a && !a.error && a.d && a.label === level) return `${level} ${a.d.toFixed(2)}`
  return level
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

// compactTokens writes a token count short enough for a band: 840, 2k, 84k,
// 1M, 1.5M, as the daemon does.
function compactTokens(n: number): string {
  if (n < 1000) return `${n}`
  if (n < 1_000_000) return `${Math.round(n / 1000)}k`
  return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, '')}M`
}

// contextOf writes the context window line of a worker, and the color of the
// percentage: warning at 70 percent and over, error at 90 percent and over.
// A harness that reports no context window (agy) shows the token count alone.
// It is undefined when the harness has not reported any tokens.
function contextOf(w: AgentNetWorker): { text: string; color?: 'warning' | 'error' } | undefined {
  if (!(w.contextTokens > 0)) return undefined
  if (!(w.contextLimit > 0)) return { text: `ctx ${compactTokens(w.contextTokens)}` }
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

// The model keeps the name the harness reported, without the provider prefix
// and, as before, without a leading claude-. It is never shortened for width.
function displayModel(model: string): string {
  return modelName(model).replace(/^claude-/, '')
}

export type WorkerRow = {
  sessionId: string
  symbol: string
  symbolColor: 'accent' | 'success' | 'error' | 'dim'
  title: string
  harness: string
  model: string
  difficulty: string
  runTime: string
  last: string
  lastColor?: 'warning' | 'error' | 'success' | 'dim'
  line: string
}

// Symbol and end state of a worker: running, done, a failed end or a start
// that still waits in the queue.
function stateOf(w: AgentNetWorker): { symbol: string; color: WorkerRow['symbolColor']; ended: boolean } {
  if (w.endStatus !== '') {
    if (w.endStatus === 'done') return { symbol: '✓', color: 'success', ended: true }
    return { symbol: '✗', color: 'error', ended: true }
  }
  if (w.status === 'failed') return { symbol: '✗', color: 'error', ended: false }
  if (w.status === 'queued' || w.status === 'starting') return { symbol: '◌', color: 'dim', ended: false }
  return { symbol: '●', color: 'accent', ended: false }
}

// checkText is the last column of a worker that ended done while its
// project's hand-back check has not ended: the notice follows the check.
function checkText(check: string): string {
  return check === 'running' ? 'checking' : 'check queued'
}

// One row per worker:
// " <symbol> <title padded>  <harness> <model, full>  <run time>  <ctx or end>"
// The title takes the remaining width. On a narrow pane the context column is
// dropped before the run time, and the model is never cut.
export function formatWorkerRows(workers: AgentNetWorker[], now: number, columns: number): WorkerRow[] {
  const gap = 2
  const prepared = workers.map(w => {
    const state = stateOf(w)
    const ended = w.endStatus !== ''
    const ctx = ended ? undefined : contextOf(w)
    const runTime = shortDuration(((ended ? w.endedAt : now) - w.startedAt) * 1000)
    const harness = w.harness
    const model = displayModel(w.model)
    const difficulty = w.difficulty
    const tail = `${harness} ${model}${difficulty ? ' '.repeat(gap) + difficulty : ''}${' '.repeat(gap)}${runTime}`
    if (ended && w.check !== '') {
      return { w, state, harness, model, difficulty, runTime, tail, last: checkText(w.check), lastColor: 'dim' as WorkerRow['lastColor'], lastIsContext: false }
    }
    if (ended) {
      return { w, state, harness, model, difficulty, runTime, tail, last: w.endStatus, lastColor: (w.endStatus === 'done' ? 'success' : 'error') as WorkerRow['lastColor'], lastIsContext: false }
    }
    return { w, state, harness, model, difficulty, runTime, tail, last: ctx?.text ?? '', lastColor: ctx?.color, lastIsContext: ctx !== undefined }
  })

  // One title column for every row, so the harness, model and run time line
  // up. At a narrow width the context column goes before the run time; an end
  // state stays, because it is the reason the row is there.
  const maxTail = Math.max(0, ...prepared.map(r => r.tail.length))
  const maxContext = Math.max(0, ...prepared.filter(r => r.lastIsContext).map(r => r.last.length))
  const minTitle = 12
  const showContext = maxContext > 0 && columns >= 2 + gap + maxTail + gap + maxContext + minTitle
  let maxLast = 0
  for (const row of prepared) {
    if (row.lastIsContext && !showContext) {
      row.last = ''
      row.lastColor = undefined
    }
    maxLast = Math.max(maxLast, row.last.length)
  }
  const titleWidth = Math.max(0, columns - (2 + gap + maxTail + (maxLast > 0 ? gap + maxLast : 0)))

  return prepared.map(row => {
    const title = clip(row.w.title, titleWidth)
    const paddedTitle = title + ' '.repeat(Math.max(0, titleWidth - Array.from(title).length))
    const line = `${row.state.symbol} ` + (titleWidth > 0 ? paddedTitle + ' '.repeat(gap) : '') + row.tail +
      (row.last ? ' '.repeat(gap) + row.last : '')
    return {
      sessionId: row.w.sessionId,
      symbol: row.state.symbol,
      symbolColor: row.state.color,
      title: paddedTitle,
      harness: row.harness,
      model: row.model,
      difficulty: row.difficulty,
      runTime: row.runTime,
      last: row.last,
      lastColor: row.lastColor,
      line,
    }
  })
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
  const title = w.title ? ` (${w.title})` : ''
  const run = noticeDuration((w.endedAt - w.startedAt) * 1000)
  const what = w.endNote || w.task
  const check = w.check ? ` (${checkText(w.check)})` : ''
  return `agent-net worker ${who}${title} ${w.endStatus}${check} · ${run}` + (what ? `: ${what}` : '')
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

type PollState = { lastGoodAt?: number; failedAt?: number }

async function failedPoll($: EngineInterface, state: PollState, error: string): Promise<void> {
  if (state.lastGoodAt === undefined) {
    $.ui.status(`agent-net: ${error}`)
    return
  }
  const now = await $.clock.now()
  state.failedAt ??= now
  const elapsedMs = now - state.failedAt
  if (elapsedMs <= 15_000) return

  const seconds = Math.floor(elapsedMs / 1000)
  await update($, staleSecondsAtom, () => seconds)
  const rows = await read($, workersAtom)
  const status = statusText(rows)
  $.ui.status(status ? `${status} · not updated for ${seconds}s` : undefined)
}

async function poll($: EngineInterface, sessionId: string, state: PollState): Promise<void> {
  try {
    let result: Awaited<ReturnType<typeof $.process.run>> | undefined
    let failure = ''
    for (const path of binaryPaths()) {
      try {
        result = await $.process.run(
          [path, 'workers', '-json', '-recent', '-owner-claude', sessionId],
          { timeoutMs: RUN_TIMEOUT_MS },
        )
        if (result.exitCode !== 127) break
        failure = result.stderr || `not found at ${path}`
      } catch (error) {
        failure = `could not run ${path}: ${String(error)}`
      }
    }
    if (!result) { await failedPoll($, state, failure); return }
    const { exitCode, stdout, stderr } = result
    if (exitCode !== 0) {
      await failedPoll($, state, stderr || failure || `workers exited with code ${exitCode}`)
      return
    }
    const parsed = JSON.parse(stdout) as { workers?: RawWorker[]; panel_version?: string }
    const rows: AgentNetWorker[] = []
    for (const raw of parsed.workers ?? []) {
      const worker = workerOf(raw)
      if (worker && isVisible(worker)) rows.push(worker)
    }
    state.lastGoodAt = await $.clock.now()
    state.failedAt = undefined
    await update($, outdatedAtom, () => Boolean(parsed.panel_version && parsed.panel_version !== PANEL_VERSION))
    await update($, workersAtom, () => rows)
    await update($, staleSecondsAtom, () => 0)
    $.ui.status(statusText(rows))
    await notifyEnds($, rows)
  } catch (error) {
    await failedPoll($, state, String(error))
  }
}

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    const result = await next(e)
    const sessionId = await $.session.id()
    const state: PollState = {}
    const tick = () => {
      void poll($, sessionId, state)
    }
    tick()
    $.clock.every(POLL_MS, tick)
    return result
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    const workers = (await read($, workersAtom)).filter(isVisible)
    const outdated = await read($, outdatedAtom)
    if ((!outdated && workers.length === 0) || e.props.hasSurvey) {
      return next(e)
    }

    const now = Math.floor((await $.clock.now()) / 1000)
    const columns = e.props.bodyColumns
    const { Box, Text } = $.ui.resolve(e)

    const staleSeconds = await read($, staleSecondsAtom)
    const staleSuffix = staleSeconds === 0 ? '' : ` · not updated for ${staleSeconds}s`
    const label = ` agent-net workers${staleSuffix} `
    const rule = '─'.repeat(2) + label + '─'.repeat(Math.max(0, columns - 2 - label.length))
    return (
      <Box flexDirection="column">
        <Text dimColor wrap="truncate-end">{rule}</Text>
        {outdated && <Text dimColor>agent-net panel is out of date: run /reload-plugins or restart Claude Code</Text>}
        {formatWorkerRows(workers, now, columns).map(row => (
          <Box key={row.sessionId}>
            <Text wrap="truncate-end">
              <Text color={row.symbolColor === 'dim' ? undefined : row.symbolColor} dimColor={row.symbolColor === 'dim'}>{row.symbol}</Text>
              <Text> {row.title}{row.title.length > 0 ? '  ' : ''}</Text>
              <Text dimColor>{row.harness} {row.model}{row.difficulty ? '  ' + row.difficulty : ''}  {row.runTime}</Text>
              <Text color={row.lastColor}>{row.last ? '  ' + row.last : ''}</Text>
            </Text>
          </Box>
        ))}
      </Box>
    )
  })
}
