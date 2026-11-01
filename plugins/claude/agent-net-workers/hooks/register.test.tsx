import { expect, mock, test } from 'claude-code/testing'
import { formatWorkerRows } from './register'
import type { AgentNetWorker } from '../types'

const NOW = 1_700_000_000_000

const BAND = {
  component: 'AbovePrompt',
  requestId: 'agent-net-workers',
  props: {
    hasSurvey: false,
    isWorking: false,
    maxRows: 8,
    bodyColumns: 80,
    scroll: { offset: 0, bodyRows: 7 },
    view: {},
  },
} as const

function runResult(stdout: string) {
  return { exitCode: 0, stdout, stderr: '', isStdoutTruncated: false, isStderrTruncated: false }
}

test('the band lists a running worker and the status line counts it', async ($, on) => {
  const clock = mock.clock(on, { now: NOW })
  const statuses: Array<string | undefined> = []
  let argv: readonly string[] = []
  on('process.run', (_, e) => {
    argv = e.argv
    return {
      value: runResult(JSON.stringify({
        workers: [{
          session_id: 's_abcdefab12',
          harness: 'claude',
          model: 'anthropic/claude-sonnet-5',
          project: 'shop',
          status: 'busy',
          online: true,
          started_at: Math.floor(NOW / 1000) - 120,
          task: 'write the tests',
        }],
      })),
    }
  })
  on('ui.status', (_, e) => {
    statuses.push(e.text)
    return { value: undefined }
  })

  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('session.id', () => ({ value: 'test-session' }))
  await $.session.start({ cwd: '/work', surface: 'terminal', isInteractive: true })
  await clock.settle()
  expect(argv).toEqual(['agent-net', 'workers', '-json', '-recent', '-owner-claude', 'test-session'])

  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await $.ui.mount({ plugin: 'agent-net-workers', surface, ...BAND })
    expect(await ui.find({ type: 'Text', text: /claude +sonnet-5 +2m/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /write the tests/ })).toBeDefined()
    await ui.unmount()
  }
  expect(statuses.at(-1)).toBe('agent-net: 1 busy')
})

test('an ended worker shows its end note, and none clears the line', async ($, on) => {
  const clock = mock.clock(on, { now: NOW })
  const session = mock.session(on)
  const statuses: Array<string | undefined> = []
  let payload = {
    workers: [{
      session_id: 's_abcdefcd34',
      harness: 'opencode',
      model: 'deepseek-v4.1-flash',
      project: 'shop',
      status: 'done',
      online: false,
      started_at: Math.floor(NOW / 1000) - 300,
      task: 'fix the parser',
      ended_at: Math.floor(NOW / 1000),
      end_status: 'done',
      end_note: 'finished the parser',
    }],
  }
  on('process.run', () => ({ value: runResult(JSON.stringify(payload)) }))
  on('ui.status', (_, e) => {
    statuses.push(e.text)
    return { value: undefined }
  })

  on('ui.render', ($, e) => {
    const { Text } = $.ui.resolve(e)
    return <Text>engine</Text>
  })
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('session.id', () => ({ value: 'test-session' }))
  await $.session.start({ cwd: '/work', surface: 'terminal', isInteractive: true })
  await clock.settle()

  let ui = await $.ui.mount({ plugin: 'agent-net-workers', surface: 'terminal', ...BAND })
  expect(await ui.find({ type: 'Text', text: /finished the parser/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /fix the parser/ })).toBeUndefined()
  await ui.unmount()
  expect(statuses.at(-1)).toBe('agent-net: 1 done')

  // One notice was appended, with both parts of the run time.
  let appended = session.appended()
  expect(appended.length).toBe(1)
  expect(appended[0]?.message.type).toBe('system')
  expect(appended[0]?.message.content).toEqual([{
    type: 'text',
    text: 'agent-net worker opencode · deepseek-v4.1-flash done · 5m 0s: finished the parser',
  }])

  // The next poll does not append it again.
  await clock.advance(3000)
  expect(session.appended().length).toBe(1)

  // Two minutes later the daemon no longer reports it: the band goes away,
  // and no second notice is written.
  payload = { workers: [] }
  await clock.advance(3000)
  ui = await $.ui.mount({ plugin: 'agent-net-workers', surface: 'terminal', ...BAND })
  expect(await ui.find({ type: 'Text', text: /finished the parser/ })).toBeUndefined()
  await ui.unmount()
  expect(statuses.at(-1)).toBeUndefined()
  expect(session.appended().length).toBe(1)
})

test('a missing or failing agent-net shows nothing', async ($, on) => {
  const clock = mock.clock(on, { now: NOW })
  const statuses: Array<string | undefined> = []
  on('process.run', () => ({
    value: { exitCode: 127, stdout: '', stderr: 'agent-net: not found', isStdoutTruncated: false, isStderrTruncated: false },
  }))
  on('ui.status', (_, e) => {
    statuses.push(e.text)
    return { value: undefined }
  })

  on('ui.render', ($, e) => {
    const { Text } = $.ui.resolve(e)
    return <Text>engine</Text>
  })
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('session.id', () => ({ value: 'test-session' }))
  await $.session.start({ cwd: '/work', surface: 'terminal', isInteractive: true })
  await clock.settle()

  const ui = await $.ui.mount({ plugin: 'agent-net-workers', surface: 'terminal', ...BAND })
  expect(await ui.find({ type: 'Text', text: /○|✓|✗/ })).toBeUndefined()
  await ui.unmount()
  expect(statuses.at(-1)).toBeUndefined()
})

test('the band adds the context window after the run time', async ($, on) => {
  const clock = mock.clock(on, { now: NOW })
  on('process.run', () => ({
    value: runResult(JSON.stringify({
      workers: [{
        session_id: 's_abcdefa1',
        harness: 'opencode',
        model: 'deepseek-v4.1-flash',
        project: 'shop',
        status: 'busy',
        online: true,
        started_at: Math.floor(NOW / 1000) - 120,
        task: 'write the tests',
        context_tokens: 84000,
        context_limit: 1000000,
        tokens: 345000,
      }, {
        session_id: 's_abcdefa2',
        harness: 'claude',
        model: 'claude-sonnet-5',
        project: 'shop',
        status: 'busy',
        online: true,
        started_at: Math.floor(NOW / 1000) - 60,
        task: 'read the spec',
        context_tokens: 950000,
        context_limit: 1000000,
        tokens: 1200000,
      }],
    })),
  }))
  on('ui.status', () => ({ value: undefined }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('session.id', () => ({ value: 'test-session' }))
  await $.session.start({ cwd: '/work', surface: 'terminal', isInteractive: true })
  await clock.settle()

  const ui = await $.ui.mount({ plugin: 'agent-net-workers', surface: 'terminal', ...BAND })
  expect(await ui.find({ type: 'Text', text: /ctx 8%/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /ctx 95%/ })).toBeDefined()
  await ui.unmount()
})

test('a refused notice is ignored and not tried again', async ($, on) => {
  const clock = mock.clock(on, { now: NOW })
  let attempts = 0
  const payload = {
    workers: [{
      session_id: 's_abcdefef56',
      harness: 'claude',
      model: 'claude-sonnet-5',
      project: 'shop',
      status: 'failed',
      online: false,
      started_at: Math.floor(NOW / 1000) - 42,
      task: 'run the tests',
      ended_at: Math.floor(NOW / 1000),
      end_status: 'failed',
      end_note: 'the harness process ended by itself, with no hand-back',
    }],
  }
  on('process.run', () => ({ value: runResult(JSON.stringify(payload)) }))
  on('session.append', () => {
    attempts += 1
    return { deny: 'not for this session' }
  })
  on('ui.status', () => ({ value: undefined }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('session.id', () => ({ value: 'test-session' }))

  await $.session.start({ cwd: '/work', surface: 'terminal', isInteractive: true })
  await clock.settle()
  expect(attempts).toBe(1)

  await clock.advance(3000)
  expect(attempts).toBe(1)
})

function running(overrides: Partial<AgentNetWorker> = {}): AgentNetWorker {
  return {
    sessionId: 's_hidden1234', harness: 'codex', model: 'openai/gpt-6.1-sol:medium',
    project: 'shop', status: 'busy', online: true, startedAt: NOW / 1000 - 720,
    task: 'Build part 5 of the documentation', contextTokens: 105000, contextLimit: 475000,
    tokens: 0, endedAt: 0, endStatus: '', endNote: '', ...overrides,
  }
}

test('worker columns omit ids, busy and token counts and align every task', () => {
  const rows = formatWorkerRows([
    running(),
    running({ harness: 'claude', model: 'anthropic/claude-sonnet-5', status: 'idle',
      contextTokens: 0, contextLimit: 0, task: 'Read the specification', startedAt: NOW / 1000 - 42 }),
    running({ harness: 'opencode', model: 'provider/deepseek-v4.1-flash', status: 'needs you', task: 'Check the parser' }),
  ], NOW / 1000, 100)
  const lines = rows.map(r => r.fields.join('') + r.task)
  expect(lines[0]?.includes('1234')).toBe(false)
  expect(lines[0]?.includes('busy')).toBe(false)
  expect(lines[0]?.includes('○')).toBe(false)
  expect(lines[0]?.includes('105k')).toBe(false)
  expect(lines[0]?.includes('ctx 22%')).toBe(true)
  expect(lines[0]?.includes('gpt-6.1-sol:medium')).toBe(true)
  expect(lines[1]?.includes('sonnet-5')).toBe(true)
  expect(lines[1]?.includes('anthropic/')).toBe(false)
  expect(lines[1]?.includes('idle')).toBe(true)
  expect(rows[1]?.statusColor).toBe('warning')
  expect(rows[2]?.statusColor).toBe('warning')
  const taskColumn = lines[0]!.indexOf('Build')
  expect(lines[1]!.indexOf('Read')).toBe(taskColumn)
  expect(lines[2]!.indexOf('Check')).toBe(taskColumn)
})

test('worker tasks use the remaining width, end with an ellipsis and stay on one line', () => {
  for (const width of [48, 80, 100]) {
    const row = formatWorkerRows([running({ task: 'Build '.repeat(40) + '\nsecond line' })], NOW / 1000, width)[0]!
    const line = row.fields.join('') + row.task
    expect(Array.from(line).length).toBe(width)
    expect(line.endsWith('…')).toBe(true)
    expect(line.includes('\n')).toBe(false)
  }
})

test('worker model shortening drops effort then whole segments as the TUI does', () => {
  const row = formatWorkerRows([running({ model: 'provider/claude-sonnet-5:very-long-effort' })], NOW / 1000, 100)[0]!
  expect(row.fields[1]?.trim()).toBe('sonnet-5')
  const long = formatWorkerRows([running({ model: 'provider/deepseek-v4.1-flash-preview-extra' })], NOW / 1000, 100)[0]!
  expect(long.fields[1]?.trim()).toBe('deepseek-v4.1-flash…')
})

test('exception states and high context have visible colors', () => {
  for (const status of ['idle', 'waiting', 'needs you', 'starting', 'queued', 'stopping']) {
    const row = formatWorkerRows([running({ status })], NOW / 1000, 100)[0]!
    expect(row.fields[2]?.trim()).toBe(status)
    expect(row.statusColor).toBe('warning')
  }
  const rows = formatWorkerRows([
    running({ contextTokens: 70000, contextLimit: 100000 }),
    running({ contextTokens: 90000, contextLimit: 100000 }),
    running({ endStatus: 'done', endedAt: NOW / 1000 }),
    running({ endStatus: 'failed', endedAt: NOW / 1000 }),
  ], NOW / 1000, 100)
  expect(rows[0]?.contextColor).toBe('warning')
  expect(rows[1]?.contextColor).toBe('error')
  expect(rows[2]?.statusColor).toBe('success')
  expect(rows[3]?.statusColor).toBe('error')
})
