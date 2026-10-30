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
  endedAt: number
  endStatus: string
  endNote: string
}

declare module 'claude-code' {
  interface PluginState {
    'agent-net-workers': { workers: AgentNetWorker[]; notified: string[] }
  }
}
