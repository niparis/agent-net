import { expect, mock, test } from 'claude-code/testing'

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
    expect(await ui.find({ type: 'Text', text: /ab12 claude · claude-sonnet-5 busy 2m/ })).toBeDefined()
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
