export type PeerMember = {
  role: string
  agent?: string
  exited?: boolean
  worker?: boolean
  model?: string
  effort?: string
  worktree?: string
  branch?: string
  base?: string
  kicked?: boolean
  pid?: number
  unread?: number
}

export type PeerRoom = {
  id: string
  repo: string
  members: PeerMember[]
  started_at: string
  ended_at?: string
  ended_reason?: string
}

export type PeerMessage = { id: string; at: string; from: string; to: string; text: string }

export type PeerLogEntry = { at?: string; kind: string; text: string }

export type PeerMark = { id: string; at: string }

export type PeerState = {
  rooms: Record<string, PeerRoom>
  messages: Record<string, PeerMessage[]>
  selected: string | null
  // 'chat', or the role whose log the pane shows.
  tab: string
  confirm: string | null
  led: string[]
  watchError: string | null
  log: PeerLogEntry[]
  delivered: Record<string, PeerMark | null>
  notified: string[]
  // Pane: messages shown whole, and messages per room when the pane was
  // last on screen.
  expanded: string[]
  seen: Record<string, number>
}

declare module 'claude-code' {
  interface PluginState {
    peer: { state: PeerState }
  }
}
