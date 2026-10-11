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

export const sides = ["top", "right", "bottom", "left"] as const
type Side = (typeof sides)[number]
type Box = { x: number; y: number; w: number; h: number }

// The sides of `a` and `b` that face each other: along the axis with the larger gap between them,
// so nodes side by side join right to left and nodes in a column join bottom to top.
export function facingSides(a: Box, b: Box): [Side, Side] {
	const dx = b.x + b.w / 2 - (a.x + a.w / 2)
	const dy = b.y + b.h / 2 - (a.y + a.h / 2)
	const gapX = Math.abs(dx) - (a.w + b.w) / 2
	const gapY = Math.abs(dy) - (a.h + b.h) / 2
	if (gapX > gapY) {
		return dx >= 0 ? ["right", "left"] : ["left", "right"]
	}

	return dy >= 0 ? ["bottom", "top"] : ["top", "bottom"]
}

// Boxes in board coordinates: a child's position counts from its frame.
function absoluteBoxes(nodes: FlowNode[]): Map<string, Box> {
	const byId = new Map(nodes.map((n) => [n.id, n]))
	const boxes = new Map<string, Box>()
	for (const node of nodes) {
		let { x, y } = node.position
		let parent = node.parentId ? byId.get(node.parentId) : undefined
		for (let guard = 0; parent && guard < nodes.length; guard++) {
			x += parent.position.x
			y += parent.position.y
			parent = parent.parentId ? byId.get(parent.parentId) : undefined
		}
		const w = node.measured?.width ?? node.width ?? 0
		const h = node.measured?.height ?? node.height ?? 0
		boxes.set(node.id, { x, y, w, h })
	}

	return boxes
}

// Edges store no route: each one attaches to the facing sides of its nodes where they are now,
// also while a node is dragged.
export function withSides(edges: FlowEdge[], nodes: FlowNode[]): FlowEdge[] {
	const boxes = absoluteBoxes(nodes)

	return edges.map((edge) => {
		const from = boxes.get(edge.source)
		const to = boxes.get(edge.target)
		if (!from || !to) {
			return edge
		}
		const [sourceHandle, targetHandle] = facingSides(from, to)
		const same =
			edge.sourceHandle === sourceHandle && edge.targetHandle === targetHandle

		return same ? edge : { ...edge, sourceHandle, targetHandle }
	})
}
