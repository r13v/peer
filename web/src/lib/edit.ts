import { isInside, nodeMap } from "./board.ts"
import { arrangeFrame } from "./layout.ts"
import type { Board, FrameNode, Op } from "./types.ts"

// Delete ops for a selection. An entity that the server deletes implicitly with another one
// (edges of a deleted node, the content of a cascaded frame) gets no op of its own.
export function deleteOps(
	board: Board,
	nodeIds: string[],
	edgeIds: string[],
	cascade: boolean,
): Op[] {
	const nodes = nodeMap(board)
	const cascaded = cascade
		? nodeIds.filter((id) => nodes.get(id)?.type === "frame")
		: []
	const isGone = (id: string) =>
		nodeIds.includes(id) || cascaded.some((f) => isInside(nodes, id, f))
	const nodeOps = nodeIds
		.filter((id) => !cascaded.some((f) => isInside(nodes, id, f)))
		.map(
			(id): Op =>
				cascaded.includes(id)
					? { op: "delete", id, cascade: true }
					: { op: "delete", id },
		)
	const edgeOps = board.edges
		.filter((e) => edgeIds.includes(e.id) && !isGone(e.from) && !isGone(e.to))
		.map((e): Op => ({ op: "delete", id: e.id }))

	return [...nodeOps, ...edgeOps]
}

// Only the fields that differ from the stored node; undefined when nothing changed.
export function changedSet(
	current: Record<string, unknown>,
	next: Record<string, number | string | null>,
): Record<string, unknown> | undefined {
	const set = Object.fromEntries(
		Object.entries(next).filter(
			([field, value]) => (current[field] ?? null) !== value,
		),
	)

	return Object.keys(set).length > 0 ? set : undefined
}

export function arrangeOps(board: Board, frame: FrameNode): Op[] {
	const { positions, frameSize } = arrangeFrame(board, frame)
	const ops: Op[] = []
	for (const node of board.nodes) {
		const p = positions.get(node.id)
		const set = p && changedSet(node, { x: p.x, y: p.y })
		if (set) {
			ops.push({ op: "update", id: node.id, set })
		}
	}
	if (frameSize) {
		ops.push({
			op: "update",
			id: frame.id,
			set: { w: frameSize.w, h: frameSize.h },
		})
	}

	return ops
}
