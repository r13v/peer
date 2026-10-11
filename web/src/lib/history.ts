import { depthOf, nodeMap } from "./board.ts"
import type {
	ApplyResult,
	Board,
	BoardEdge,
	BoardEvent,
	BoardNode,
	Op,
	Snapshot,
	Thread,
} from "./types.ts"

// One undo or redo step: the pack that reverts a pack of this tab and the revisions it expects.
export type Step = { ops: Op[]; expect: Record<string, number> }

const nodeFields = [
	"text",
	"color",
	"caption",
	"title",
	"order",
	"parentId",
	"x",
	"y",
	"w",
	"h",
	"z",
]
const edgeFields = ["from", "to", "label"]

type Entities = {
	nodes: Map<string, BoardNode>
	edges: Map<string, BoardEdge>
	threads: Map<string, Thread>
}

function entities(board: Board): Entities {
	return {
		nodes: nodeMap(board),
		edges: new Map(board.edges.map((e) => [e.id, e])),
		threads: new Map(board.threads.map((t) => [t.id, t])),
	}
}

export function touchedIds(result: ApplyResult): string[] {
	return [...new Set([...result.created, ...result.changed, ...result.deleted])]
}

// Every touched entity, deleted ones included, has rev == revision after the pack.
export function expectFor(result: ApplyResult): Record<string, number> {
	return Object.fromEntries(
		touchedIds(result).map((id) => [id, result.revision]),
	)
}

export function revOf(board: Board, id: string): number | undefined {
	const entity =
		board.nodes.find((n) => n.id === id) ??
		board.edges.find((e) => e.id === id) ??
		board.threads.find((t) => t.id === id) ??
		board.deleted.find((d) => d.id === id)

	return entity?.rev
}

// Values to set to return `after` to `before`; null removes a field that `before` did not have.
function revertSet(
	before: object,
	after: object,
	fields: string[],
): Record<string, unknown> | undefined {
	const was = before as Record<string, unknown>
	const now = after as Record<string, unknown>
	const set: Record<string, unknown> = {}
	for (const field of fields) {
		if (was[field] !== now[field]) {
			set[field] = was[field] ?? null
		}
	}

	return Object.keys(set).length > 0 ? set : undefined
}

// The pack that turns `after` back into `before` for the touched entities. Order: restore nodes
// parents first, then edges and threads; revert updates; delete edges and threads, then nodes
// children first. A reply changes no revertible field, so a reply-only pack inverts to nothing.
export function invertPack(
	before: Board,
	after: Board,
	touched: string[],
): Op[] {
	const was = entities(before)
	const now = entities(after)
	const restoreNodes: BoardNode[] = []
	const restoreOthers: Op[] = []
	const updates: Op[] = []
	const deleteOthers: Op[] = []
	const deleteNodes: string[] = []

	for (const id of touched) {
		const wasNode = was.nodes.get(id)
		const nowNode = now.nodes.get(id)
		const wasEdge = was.edges.get(id)
		const nowEdge = now.edges.get(id)
		const wasThread = was.threads.get(id)
		const nowThread = now.threads.get(id)

		if (wasNode && nowNode) {
			const set = revertSet(wasNode, nowNode, nodeFields)
			if (set) {
				updates.push({ op: "update", id, set })
			}
		} else if (wasNode) {
			restoreNodes.push(wasNode)
		} else if (nowNode) {
			deleteNodes.push(id)
		}

		if (wasEdge && nowEdge) {
			const set = revertSet(wasEdge, nowEdge, edgeFields)
			if (set) {
				updates.push({ op: "update", id, set })
			}
		} else if (wasEdge) {
			restoreOthers.push({ op: "restore", edge: wasEdge })
		} else if (nowEdge) {
			deleteOthers.push({ op: "delete", id })
		}

		if (wasThread && nowThread) {
			if (wasThread.resolved !== nowThread.resolved) {
				updates.push({
					op: "resolve",
					thread: id,
					resolved: wasThread.resolved,
				})
			}
		} else if (wasThread) {
			restoreOthers.push({ op: "restore", thread: wasThread })
		} else if (nowThread) {
			deleteOthers.push({ op: "delete", id })
		}
	}

	restoreNodes.sort(
		(a, b) => depthOf(was.nodes, a.id) - depthOf(was.nodes, b.id),
	)
	deleteNodes.sort((a, b) => depthOf(now.nodes, b) - depthOf(now.nodes, a))
	// Restored edges come after all restored nodes, so threads sort after edges.
	restoreOthers.sort((a, b) => Number("thread" in a) - Number("thread" in b))
	const inverse: Op[] = [
		...restoreNodes.map((node): Op => ({ op: "restore", node })),
		...restoreOthers,
		...updates,
		...deleteOthers,
		...deleteNodes.map((id): Op => ({ op: "delete", id })),
	]

	return inverse
}

// Sets `revision` for `ids` in every step that expects them. With `previous`, only an expectation
// equal to the previous rev moves: then nobody else changed the entity in between.
export function rewriteExpect(
	steps: Step[],
	ids: string[],
	revision: number,
	previous?: Record<string, number | undefined>,
): Step[] {
	const rewritten = steps.map((step) => {
		const expect = { ...step.expect }
		for (const id of ids) {
			const canMove = id in expect && (!previous || expect[id] === previous[id])
			if (canMove) {
				expect[id] = revision
			}
		}

		return { ...step, expect }
	})

	return rewritten
}

// Only the current snapshot counts as known: a takeover or a checkout can bring back bytes
// whose hash the tab saw before.
export function needsReload(
	current: Snapshot | undefined,
	event: BoardEvent,
): boolean {
	return event.hash !== current?.hash
}

// A reloaded snapshot that differs from the current one without a newer revision was written
// outside the revision sequence (SSE section of docs/BOARD_API.md): the history no longer applies.
export function historyInvalidated(
	current: Snapshot | undefined,
	next: Snapshot,
	lastRevision: number,
): boolean {
	return (
		current !== undefined &&
		next.hash !== current.hash &&
		next.board.revision <= lastRevision
	)
}
