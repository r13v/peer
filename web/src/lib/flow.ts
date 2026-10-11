import type { Edge, Node } from "@xyflow/react"
import { depthOf, isPlaced, nodeMap } from "./board.ts"
import { type Placement, sizeOf } from "./layout.ts"
import type { Board, BoardEdge, BoardNode } from "./types.ts"

export type FlowNode = Node<{ node: BoardNode }, BoardNode["type"]>
export type FlowEdge = Edge<{ edge: BoardEdge }, "board">

// xyflow needs a parent before its children. `z` sets the render order through zIndex; xyflow
// lifts a child whose zIndex is not above its parent's to parent + 1, so a frame stays under
// its own children. An unplaced node renders invisibly at a provisional position until the
// tab has measured and placed it.
export function toFlowNodes(
	board: Board,
	placement: Placement,
	selected: Set<string>,
): FlowNode[] {
	const nodes = nodeMap(board)
	const ordered = [...board.nodes].sort(
		(a, b) =>
			depthOf(nodes, a.id) - depthOf(nodes, b.id) || (a.z ?? 0) - (b.z ?? 0),
	)
	const flowNodes = ordered.map((node): FlowNode => {
		const position = isPlacedAt(node) ??
			placement.positions.get(node.id) ?? { x: 0, y: 0 }
		const size = sizeOf(node, placement.sizes)
		const flowNode: FlowNode = {
			id: node.id,
			type: node.type,
			position,
			parentId: node.parentId,
			data: { node },
			selected: selected.has(node.id),
			width: size.w,
			zIndex: node.z ?? 0,
		}
		if (!isPlaced(node)) {
			flowNode.className = "unplaced"
		}
		// A card grows with its text; `h` is its minimum height.
		if (node.type !== "card") {
			flowNode.height = size.h
		}
		if (node.type === "frame") {
			flowNode.dragHandle = ".frame-title"
		}

		return flowNode
	})

	return flowNodes
}

function isPlacedAt(node: BoardNode): { x: number; y: number } | undefined {
	if (node.x === undefined || node.y === undefined) {
		return undefined
	}

	return { x: node.x, y: node.y }
}

export function toFlowEdges(board: Board, selected: Set<string>): FlowEdge[] {
	return board.edges.map((edge) => ({
		id: edge.id,
		source: edge.from,
		target: edge.to,
		type: "board",
		data: { edge },
		selected: selected.has(edge.id),
	}))
}
