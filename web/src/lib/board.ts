import type { Board, BoardNode, Target } from "./types.ts"

export function isPlaced(node: BoardNode): boolean {
	return node.x !== undefined && node.y !== undefined
}

export function nodeMap(board: Board): Map<string, BoardNode> {
	return new Map(board.nodes.map((n) => [n.id, n]))
}

// Number of frames above the node; a node on the board root has depth 0.
export function depthOf(nodes: Map<string, BoardNode>, id: string): number {
	let depth = 0
	let parent = nodes.get(id)?.parentId
	while (parent && depth <= nodes.size) {
		depth++
		parent = nodes.get(parent)?.parentId
	}

	return depth
}

export function isInside(
	nodes: Map<string, BoardNode>,
	id: string,
	ancestorId: string,
): boolean {
	let parent = nodes.get(id)?.parentId
	for (let guard = 0; parent && guard <= nodes.size; guard++) {
		if (parent === ancestorId) {
			return true
		}
		parent = nodes.get(parent)?.parentId
	}

	return false
}

export function nodeLabel(node: BoardNode): string {
	switch (node.type) {
		case "card":
			return node.text.split("\n")[0] || "(empty card)"
		case "image":
			return node.caption || "image"
		case "frame":
			return node.title || "(untitled frame)"
	}
}

export function nodeText(node: BoardNode): string {
	switch (node.type) {
		case "card":
			return node.text
		case "image":
			return node.caption ?? ""
		case "frame":
			return node.title
	}
}

export function targetFor(board: Board, id: string): Target | undefined {
	const node = board.nodes.find((n) => n.id === id)
	if (node) {
		return { kind: node.type === "frame" ? "frame" : "node", id }
	}
	if (board.edges.some((e) => e.id === id)) {
		return { kind: "edge", id }
	}

	return undefined
}

export type ThreadCount = { open: number; resolved: number }

// Threads per node and edge. Board threads and orphaned threads belong to no node or edge.
export function threadCounts(board: Board): Map<string, ThreadCount> {
	const counts = new Map<string, ThreadCount>()
	for (const thread of board.threads) {
		const { target } = thread
		if (thread.orphaned || target.kind === "board") {
			continue
		}
		const count = counts.get(target.id) ?? { open: 0, resolved: 0 }
		if (thread.resolved) {
			count.resolved++
		} else {
			count.open++
		}
		counts.set(target.id, count)
	}

	return counts
}
