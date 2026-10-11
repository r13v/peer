// Wire types of docs/BOARD_API.md.

export type CardColor = "gray" | "yellow" | "green" | "blue" | "purple" | "red"

type Geometry = { x?: number; y?: number; w?: number; h?: number; z?: number }

type CardNode = Geometry & {
	id: string
	rev: number
	parentId?: string
	type: "card"
	text: string
	color?: CardColor
}

type ImageNode = Geometry & {
	id: string
	rev: number
	parentId?: string
	type: "image"
	asset: string // "assets/<sha256>.<ext>"
	caption?: string
}

export type FrameNode = Geometry & {
	id: string
	rev: number
	parentId?: string
	type: "frame"
	title: string
	order: number
}

export type BoardNode = CardNode | ImageNode | FrameNode

export type BoardEdge = {
	id: string
	rev: number
	from: string
	to: string
	label?: string
}

export type Target =
	| { kind: "board" }
	| { kind: "frame" | "node" | "edge"; id: string }

export type Author = { role: string; roomId?: string }

type Message = {
	id: string
	author: Author
	body: string
	createdAt: string
}

export type Thread = {
	id: string
	rev: number
	target: Target
	resolved: boolean
	orphaned: boolean
	targetLabel?: string
	messages: Message[]
}

export type Board = {
	formatVersion: 1
	id: string
	title: string
	roomId?: string
	revision: number
	nodes: BoardNode[]
	edges: BoardEdge[]
	threads: Thread[]
	deleted: { id: string; rev: number }[]
	nextId: number
}

type DistributiveOmit<T, K extends PropertyKey> = T extends unknown
	? Omit<T, K>
	: never

export type NewNode = DistributiveOmit<BoardNode, "id" | "rev">

export type Op =
	| { op: "add"; ref?: string; node: NewNode }
	| {
			op: "add"
			ref?: string
			edge: { from: string; to: string; label?: string }
	  }
	| { op: "update"; id: string; set: Record<string, unknown> }
	| { op: "delete"; id: string; cascade?: boolean }
	| { op: "restore"; node?: BoardNode; edge?: BoardEdge; thread?: Thread }
	| { op: "comment"; ref?: string; target: Target; body: string }
	| { op: "reply"; thread: string; body: string }
	| { op: "resolve"; thread: string; resolved: boolean }

export type Pack =
	| { base: number; ops: Op[] }
	| { expect: Record<string, number>; ops: Op[] }

export type ApplyResult = {
	revision: number
	refs: Record<string, string>
	created: string[]
	changed: string[]
	deleted: string[]
	warnings: { kind: "roomLink"; message: string }[]
}

export type Snapshot = { board: Board; hash: string }

export type ApplyResponse = ApplyResult & Snapshot

export type BoardError =
	| {
			error: "conflict"
			base?: number
			conflicts: { id: string; rev: number | null }[]
	  }
	| { error: "invalid"; problems: { path: string; message: string }[] }
	| { error: "forbidden"; message: string }
	| { error: "notFound"; message: string }
	| { error: "unsupportedVersion"; formatVersion: number }
	| { error: "tooLarge"; limit: number }
	| { error: "unsupportedMedia"; message: string }
	| { error: "room"; message: string }

export type BoardRef = {
	key: string
	path: string
	url: string
	id?: string
	title?: string
	revision?: number
	rooms?: string[]
	mtime?: string
	error?: BoardError
}

export type CatalogGroup = {
	checkout: { path: string; name: string }
	rooms: {
		id: string
		active: boolean
		startedAt: string
		endedAt?: string
		boards: BoardRef[]
	}[]
	store: BoardRef[]
	found: BoardRef[]
}

export type Catalog = {
	workspaces: string[]
	groups: CatalogGroup[]
	outside: BoardRef[]
}

export type BoardEvent = { revision: number; hash: string; external: boolean }
