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
          title: 'Fix the parser',
          difficulty_used: 'simple',
          assessment: { d: 1.9, label: 'simple', picker_label: 'simple' },
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
    expect(await ui.find({ type: 'Text', text: /claude +sonnet-5 +simple 1\.90 +2m/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /Fix the parser/ })).toBeDefined()
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
      title: 'Fix the parser',
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
  expect(await ui.find({ type: 'Text', text: /Fix the parser/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /✓/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /done/ })).toBeDefined()
  await ui.unmount()
  expect(statuses.at(-1)).toBe('agent-net: 1 done')

  // One notice was appended, with both parts of the run time.
  let appended = session.appended()
  expect(appended.length).toBe(1)
  expect(appended[0]?.message.type).toBe('system')
  expect(appended[0]?.message.content).toEqual([{
    type: 'text',
    text: 'agent-net worker opencode · deepseek-v4.1-flash (Fix the parser) done · 5m 0s: finished the parser',
  }])

  // The next poll does not append it again.
  await clock.advance(3000)
  expect(session.appended().length).toBe(1)

  // Two minutes later the daemon no longer reports it: the band goes away,
  // and no second notice is written.
  payload = { workers: [] }
  await clock.advance(3000)
  ui = await $.ui.mount({ plugin: 'agent-net-workers', surface: 'terminal', ...BAND })
  expect(await ui.find({ type: 'Text', text: /Fix the parser/ })).toBeUndefined()
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

test('a failed poll keeps the last worker rows until a successful poll replaces them', async ($, on) => {
  const clock = mock.clock(on, { now: NOW })
  const statuses: Array<string | undefined> = []
  let calls = 0
  on('process.run', () => {
    calls += 1
    if (calls === 2) {
      return { value: { exitCode: 1, stdout: '', stderr: 'busy', isStdoutTruncated: false, isStderrTruncated: false } }
    }
    return { value: runResult(JSON.stringify({ workers: [{
      session_id: 's_keep123456', harness: 'claude', model: 'claude-sonnet-5', status: 'busy', online: true,
      started_at: Math.floor(NOW / 1000) - 120, task: 'keep this worker visible', title: 'Keep this worker visible',
    }] })) }
  })
  on('ui.status', (_, e) => { statuses.push(e.text); return { value: undefined } })
  on('ui.render', ($, e) => {
    const { Text } = $.ui.resolve(e)
    return <Text>engine</Text>
  })
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('session.id', () => ({ value: 'test-session' }))
  await $.session.start({ cwd: '/work', surface: 'terminal', isInteractive: true })
  await clock.settle()
  await clock.advance(3000)
  const ui = await $.ui.mount({ plugin: 'agent-net-workers', surface: 'terminal', ...BAND })
  expect(await ui.find({ type: 'Text', text: /Keep this worker visible/ })).toBeDefined()
  expect(statuses.at(-1)).toBe('agent-net: 1 busy')
  await ui.unmount()
  await clock.advance(3000)
  const recovered = await $.ui.mount({ plugin: 'agent-net-workers', surface: 'terminal', ...BAND })
  expect(await recovered.find({ type: 'Text', text: /Keep this worker visible/ })).toBeDefined()
  expect(statuses.at(-1)).toBe('agent-net: 1 busy')
  await recovered.unmount()
})

test('failed polls mark the last good band stale after more than 15 seconds and a good poll clears it', async ($, on) => {
  const clock = mock.clock(on, { now: NOW })
  const statuses: Array<string | undefined> = []
  let calls = 0
  on('process.run', () => {
    calls += 1
    if (calls > 1 && calls < 9) {
      return { value: { exitCode: 1, stdout: '', stderr: 'busy', isStdoutTruncated: false, isStderrTruncated: false } }
    }
    return { value: runResult(JSON.stringify({ workers: [{
      session_id: 's_stale12345', harness: 'claude', model: 'claude-sonnet-5', status: 'busy', online: true,
      started_at: Math.floor(NOW / 1000) - 120, task: 'stale worker remains visible', title: 'Stale worker remains visible',
    }] })) }
  })
  on('ui.status', (_, e) => { statuses.push(e.text); return { value: undefined } })
  on('ui.render', ($, e) => {
    const { Text } = $.ui.resolve(e)
    return <Text>engine</Text>
  })
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('session.id', () => ({ value: 'test-session' }))
  await $.session.start({ cwd: '/work', surface: 'terminal', isInteractive: true })
  await clock.settle()
  await clock.advance(21000)
  const stale = await $.ui.mount({ plugin: 'agent-net-workers', surface: 'terminal', ...BAND })
  expect(await stale.find({ type: 'Text', text: /agent-net workers · not updated for 18s/ })).toBeDefined()
  expect(await stale.find({ type: 'Text', text: /Stale worker remains visible/ })).toBeDefined()
  expect(statuses.at(-1)).toBe('agent-net: 1 busy · not updated for 18s')
  await stale.unmount()
  await clock.advance(3000)
  const fresh = await $.ui.mount({ plugin: 'agent-net-workers', surface: 'terminal', ...BAND })
  expect(await fresh.find({ type: 'Text', text: /agent-net workers/ })).toBeDefined()
  expect(await fresh.find({ type: 'Text', text: /not updated for/ })).toBeUndefined()
  expect(statuses.at(-1)).toBe('agent-net: 1 busy')
  await fresh.unmount()
})

test('a successful empty poll clears the worker band', async ($, on) => {
  const clock = mock.clock(on, { now: NOW })
  const statuses: Array<string | undefined> = []
  let calls = 0
  on('process.run', () => {
    calls += 1
    return { value: runResult(JSON.stringify(calls === 1 ? { workers: [{
      session_id: 's_empty12345', harness: 'claude', model: 'claude-sonnet-5', status: 'busy', online: true,
      started_at: Math.floor(NOW / 1000) - 120, task: 'remove this worker', title: 'Remove this worker',
    }] } : { workers: [] })) }
  })
  on('ui.status', (_, e) => { statuses.push(e.text); return { value: undefined } })
  on('ui.render', ($, e) => {
    const { Text } = $.ui.resolve(e)
    return <Text>engine</Text>
  })
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('session.id', () => ({ value: 'test-session' }))
  await $.session.start({ cwd: '/work', surface: 'terminal', isInteractive: true })
  await clock.settle()
  await clock.advance(3000)
  const ui = await $.ui.mount({ plugin: 'agent-net-workers', surface: 'terminal', ...BAND })
  expect(await ui.find({ type: 'Text', text: /remove this worker/ })).toBeUndefined()
  expect(statuses.at(-1)).toBeUndefined()
  await ui.unmount()
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

test('failed and queued workers show their symbol and title', async ($, on) => {
  const clock = mock.clock(on, { now: NOW })
  on('process.run', () => ({
    value: runResult(JSON.stringify({ workers: [{
      session_id: 's_failed1234', harness: 'opencode', model: 'deepseek-v4.1-flash', status: 'failed', online: false,
      started_at: Math.floor(NOW / 1000) - 42, task: 'run the tests', title: 'Run the tests',
      ended_at: Math.floor(NOW / 1000), end_status: 'failed',
    }, {
      session_id: 's_queued1234', harness: 'claude', model: 'claude-sonnet-5', status: 'queued', online: true,
      started_at: Math.floor(NOW / 1000) - 3, task: 'wait for room', title: 'Wait for room',
    }] })),
  }))
  on('ui.status', () => ({ value: undefined }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('session.id', () => ({ value: 'test-session' }))
  await $.session.start({ cwd: '/work', surface: 'terminal', isInteractive: true })
  await clock.settle()

  const ui = await $.ui.mount({ plugin: 'agent-net-workers', surface: 'terminal', ...BAND })
  expect(await ui.find({ type: 'Text', text: /✗/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /Run the tests/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /◌/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /Wait for room/ })).toBeDefined()
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
    task: 'Build part 5 of the documentation', title: 'Build the documentation',
    contextTokens: 105000, contextLimit: 475000,
    tokens: 0, endedAt: 0, endStatus: '', endNote: '', difficulty: '', ...overrides,
  }
}

test('worker rows show the symbol, title, full harness and model, run time and context', () => {
  const rows = formatWorkerRows([
    running(),
    running({ harness: 'claude', model: 'anthropic/claude-sonnet-5', status: 'idle', title: 'Read the spec',
      contextTokens: 0, contextLimit: 0, startedAt: NOW / 1000 - 42 }),
    running({ harness: 'opencode', model: 'provider/deepseek-v4.1-flash-preview-extra', status: 'needs you', title: 'Check the parser' }),
  ], NOW / 1000, 100)
  const lines = rows.map(r => r.line)
  expect(lines[0]?.startsWith('● ')).toBe(true)
  expect(lines[0]?.includes('Build the documentation')).toBe(true)
  expect(lines[0]?.includes('codex gpt-6.1-sol:medium')).toBe(true)
  expect(lines[0]?.includes('medium  12m')).toBe(true)
  expect(lines[0]?.includes('ctx 22%')).toBe(true)
  expect(lines[0]?.includes('1234')).toBe(false)
  expect(lines[0]?.includes('busy')).toBe(false)
  expect(lines[0]?.includes('105k')).toBe(false)
  expect(lines[1]?.includes('claude sonnet-5')).toBe(true)
  expect(lines[1]?.includes('anthropic/')).toBe(false)
  expect(lines[2]?.includes('opencode deepseek-v4.1-flash-preview-extra')).toBe(true)
  // The title is padded, so the harness starts in the same column on every row.
  const harnessColumn = lines[0]!.indexOf('codex')
  expect(lines[1]!.indexOf('claude')).toBe(harnessColumn)
  expect(lines[2]!.indexOf('opencode')).toBe(harnessColumn)
})

test('a long title is cut at the remaining width and stays on one line', () => {
  for (const width of [60, 80, 100]) {
    const row = formatWorkerRows([running({ title: 'Build '.repeat(40) + '\nsecond line' })], NOW / 1000, width)[0]!
    expect(Array.from(row.line).length).toBe(width)
    expect(row.title.trim().endsWith('…')).toBe(true)
    expect(row.line.includes('\n')).toBe(false)
  }
})

test('the model is never shortened for width', () => {
  const row = formatWorkerRows([running({ model: 'provider/deepseek-v4.1-flash-preview-extra' })], NOW / 1000, 40)[0]!
  expect(row.line.includes('deepseek-v4.1-flash-preview-extra')).toBe(true)
  expect(row.model).toBe('deepseek-v4.1-flash-preview-extra')
})

test('a worker without a context window shows its token count, not a percentage', () => {
  const rows = formatWorkerRows([
    running({ harness: 'agy', model: 'gemini-3.8-flash-low', contextTokens: 31000, contextLimit: 0 }),
  ], NOW / 1000, 100)
  expect(rows[0]?.line.includes('ctx 31k')).toBe(true)
  expect(rows[0]?.line.includes('%')).toBe(false)
  // No context window means no warning or error colour either.
  expect(rows[0]?.lastColor).toBeUndefined()
})

test('a narrow pane drops the context column before the run time', () => {
  const wide = formatWorkerRows([running()], NOW / 1000, 100)[0]!
  expect(wide.line.includes('ctx 22%')).toBe(true)
  const narrow = formatWorkerRows([running()], NOW / 1000, 50)[0]!
  expect(narrow.line.includes('ctx ')).toBe(false)
  expect(narrow.line.includes('12m')).toBe(true)
})

test('every state has its symbol, colour and last column', () => {
  const rows = formatWorkerRows([
    running(),
    running({ status: 'queued', startedAt: NOW / 1000 - 3 }),
    running({ endStatus: 'done', endedAt: NOW / 1000 }),
    running({ endStatus: 'failed', endedAt: NOW / 1000 }),
    running({ endStatus: 'stopped', endedAt: NOW / 1000 }),
    running({ endStatus: 'timed out', endedAt: NOW / 1000 }),
  ], NOW / 1000, 100)
  expect(rows[0]?.symbol).toBe('●')
  expect(rows[0]?.symbolColor).toBe('accent')
  expect(rows[1]?.symbol).toBe('◌')
  expect(rows[1]?.symbolColor).toBe('dim')
  expect(rows[2]?.symbol).toBe('✓')
  expect(rows[2]?.symbolColor).toBe('success')
  expect(rows[2]?.last).toBe('done')
  expect(rows[2]?.lastColor).toBe('success')
  for (const row of rows.slice(3)) {
    expect(row.symbol).toBe('✗')
    expect(row.symbolColor).toBe('error')
    expect(row.lastColor).toBe('error')
  }
  expect(rows[3]?.last).toBe('failed')
  expect(rows[4]?.last).toBe('stopped')
  expect(rows[5]?.last).toBe('timed out')
})

test('high context has the warning and error colours it had before', () => {
  const rows = formatWorkerRows([
    running({ contextTokens: 70000, contextLimit: 100000 }),
    running({ contextTokens: 90000, contextLimit: 100000 }),
  ], NOW / 1000, 100)
  expect(rows[0]?.lastColor).toBe('warning')
  expect(rows[1]?.lastColor).toBe('error')
})

test('a failed judge shows only the level the picker used', async () => {
  const rows = formatWorkerRows([running({ difficulty: 'normal' })], NOW / 1000, 120)
  expect(rows[0].difficulty).toBe('normal')
  expect(rows[0].line).toMatch(/sol:medium +normal +12m/)
})
