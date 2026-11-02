// One worker as `agent-net workers -json -recent -owner-claude` reports it.
export type AgentNetWorker = {
  sessionId: string
  harness: string
  model: string
  project: string
  status: string
  online: boolean
  startedAt: number
  task: string
  title: string
  contextTokens: number
  contextLimit: number
  tokens: number
  endedAt: number
  endStatus: string
  endNote: string
  // The level the picker used, with the judge's D: "normal 2.60".
  difficulty: string
}

declare module 'claude-code' {
  interface PluginState {
    'agent-net-workers': { workers: AgentNetWorker[]; notified: string[]; 'stale-seconds': number }
  }
}
