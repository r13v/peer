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

export type PeerMessage = {
	id: string
	at: string
	from: string
	to: string
	text: string
}

export type PeerLogEntry = { at?: string; kind: string; text: string }

export type PeerMark = { id: string; at: string }

// PeerBoardRef is one entry of `peer board list`; see docs/BOARD_API.md.
export type PeerBoardRef = {
	key: string
	path: string
	url: string
	title?: string
	revision?: number
	rooms?: string[]
	mtime?: string
	error?: { error: string; message?: string }
}

// PeerBoard is the part of board.json the preview draws.
export type PeerBoard = {
	title: string
	nodes: PeerBoardNode[]
	edges: { id: string; from: string; to: string; label?: string }[]
	threads: {
		target: { kind: string; id?: string }
		resolved: boolean
	}[]
}

export type PeerBoardNode = {
	id: string
	type: "card" | "image" | "frame"
	parentId?: string
	text?: string
	color?: string
	caption?: string
	title?: string
	order?: number
	x?: number
	y?: number
	w?: number
	h?: number
}

export type PeerState = {
	rooms: Record<string, PeerRoom>
	messages: Record<string, PeerMessage[]>
	selected: string | null
	// 'chat', ':boards', or the role whose log the pane shows.
	tab: string
	confirm: string | null
	led: string[]
	// own lists the rooms of this session, the only ones the plugin shows:
	// started or led here, or joined from here; a room stays once it ends.
	own: string[]
	watchError: string | null
	// ackErrors holds, per led room, why main's cursor could not move.
	ackErrors: Record<string, string>
	log: PeerLogEntry[]
	delivered: Record<string, PeerMark | null>
	notified: string[]
	// Pane: messages shown whole, and messages per room when the pane was
	// last on screen.
	expanded: string[]
	seen: Record<string, number>
}

declare module "claude-code" {
	interface PluginState {
		peer: { state: PeerState }
	}
}
