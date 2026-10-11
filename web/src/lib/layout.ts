import { Graph, layout } from "@dagrejs/dagre"
import { depthOf, isPlaced, nodeMap } from "./board.ts"
import type { Board, BoardNode, FrameNode, Op } from "./types.ts"

export type Point = { x: number; y: number }
export type Size = { w: number; h: number }
type Rect = Point & Size
type Item = Size & { id: string }

// Board units. Children of a frame start below its title bar.
const frameHeader = 44
const framePadding = 24
const blockGap = 40
const settleGap = 24

const defaultSizes: Record<BoardNode["type"], Size> = {
	card: { w: 240, h: 80 },
	image: { w: 320, h: 240 },
	frame: { w: 480, h: 320 },
}

// `known` holds sizes newer than the stored ones: fitted frames and measured unplaced nodes.
export function sizeOf(node: BoardNode, known?: Map<string, Size>): Size {
	const newer = known?.get(node.id)
	if (newer) {
		return newer
	}
	const fallback = defaultSizes[node.type]

	return { w: node.w ?? fallback.w, h: node.h ?? fallback.h }
}

// Layered layout of a block of nodes; top-left corners relative to the block's top-left corner.
function layoutBlock(
	items: Item[],
	links: [string, string][],
): Map<string, Point> {
	const graph = new Graph()
	graph.setGraph({ rankdir: "TB", nodesep: 40, ranksep: 60 })
	graph.setDefaultEdgeLabel(() => ({}))
	for (const item of items) {
		graph.setNode(item.id, { width: item.w, height: item.h })
	}
	for (const [from, to] of links) {
		graph.setEdge(from, to)
	}
	layout(graph)

	const corners = items.map((item) => {
		const center = graph.node(item.id)

		return { id: item.id, x: center.x - item.w / 2, y: center.y - item.h / 2 }
	})
	const minX = Math.min(...corners.map((c) => c.x))
	const minY = Math.min(...corners.map((c) => c.y))
	const positions = new Map(
		corners.map((c) => [
			c.id,
			{ x: Math.round(c.x - minX), y: Math.round(c.y - minY) },
		]),
	)

	return positions
}

// Where a new block goes in a container: at `origin` when it is empty, otherwise below the
// bounding box of the placed nodes, so placed nodes stay where they are and are not covered.
export function blockOrigin(placed: Rect[], origin: Point): Point {
	if (placed.length === 0) {
		return origin
	}
	const left = Math.min(...placed.map((r) => r.x))
	const bottom = Math.max(...placed.map((r) => r.y + r.h))

	return { x: left, y: bottom + blockGap }
}

// Frame size that holds `content`, never smaller than now.
function fitFrame(current: Size, content: Size): Size {
	const w = Math.max(current.w, content.w + framePadding)
	const h = Math.max(current.h, content.h + framePadding)

	return { w: Math.round(w), h: Math.round(h) }
}

type ContainerLayout = { positions: Map<string, Point>; frameSize?: Size }

function placeContainer(
	board: Board,
	container: FrameNode | undefined,
	unplaced: BoardNode[],
	placed: BoardNode[],
	known: Map<string, Size>,
): ContainerLayout {
	const positions = new Map<string, Point>()
	const rect = (n: BoardNode, p: Point): Rect => ({
		...p,
		...sizeOf(n, known),
	})
	const obstacles = placed.map((n) => rect(n, { x: n.x ?? 0, y: n.y ?? 0 }))

	if (unplaced.length > 0) {
		const ids = new Set(unplaced.map((n) => n.id))
		const items = unplaced.map((n) => ({ id: n.id, ...sizeOf(n, known) }))
		const links = board.edges
			.filter((e) => ids.has(e.from) && ids.has(e.to))
			.map((e): [string, string] => [e.from, e.to])
		const origin = container
			? { x: framePadding, y: frameHeader }
			: { x: 0, y: 0 }
		const start = blockOrigin(obstacles, origin)
		for (const [id, p] of layoutBlock(items, links)) {
			positions.set(id, { x: start.x + p.x, y: start.y + p.y })
		}
	}
	if (!container) {
		return { positions }
	}

	const rects = [
		...obstacles,
		...unplaced.map((n) => rect(n, positions.get(n.id) ?? { x: 0, y: 0 })),
	]
	const content = {
		w: Math.max(0, ...rects.map((r) => r.x + r.w)),
		h: Math.max(0, ...rects.map((r) => r.y + r.h)),
	}
	const current = sizeOf(container)
	const frameSize = fitFrame(current, content)
	const changed =
		container.w === undefined ||
		frameSize.w !== current.w ||
		frameSize.h !== current.h

	return { positions, frameSize: changed ? frameSize : undefined }
}

// Pushes placed nodes down until no node covers the top of one below it in the same column.
// A node below another keeps its x; nodes are taken from top to bottom, so a push carries on
// down the column. Nodes on one row may overlap: only a node that starts lower is pushed.
export function settleContainer(
	rects: (Rect & { id: string })[],
): Map<string, Point> {
	const moved = new Map<string, Point>()
	const order = [...rects].sort((a, b) => a.y - b.y || a.x - b.x)
	const settled: (Rect & { top: number })[] = []
	for (const rect of order) {
		let y = rect.y
		// A push can bring the node onto another node above it, so repeat until it is clear.
		for (let pushed = true; pushed; ) {
			pushed = false
			for (const above of settled) {
				const sameColumn =
					rect.x < above.x + above.w && above.x < rect.x + rect.w
				const covered = rect.y > above.top && y < above.y + above.h
				if (sameColumn && covered) {
					y = above.y + above.h + settleGap
					pushed = true
				}
			}
		}
		if (y !== rect.y) {
			moved.set(rect.id, { x: rect.x, y })
		}
		settled.push({ ...rect, y, top: rect.y })
	}

	return moved
}

// Moves and frame sizes that remove overlaps of placed nodes, container by container, deeper
// frames first: a frame grows to hold its pushed content, and then pushes its own neighbours.
// `measured` gives rendered sizes of cards and images, which may be larger than the stored ones.
export function settleOps(board: Board, measured: Map<string, Size>): Op[] {
	const nodes = nodeMap(board)
	const known = new Map(
		[...measured].filter(([id]) => nodes.get(id)?.type !== "frame"),
	)
	const sets = new Map<string, Record<string, number>>()
	const frames = board.nodes.filter((n): n is FrameNode => n.type === "frame")
	frames.sort((a, b) => depthOf(nodes, b.id) - depthOf(nodes, a.id))
	for (const container of [...frames, undefined]) {
		const children = board.nodes.filter(
			(n) => n.parentId === container?.id && isPlaced(n),
		)
		const rects = children.map((n) => ({
			id: n.id,
			x: n.x ?? 0,
			y: n.y ?? 0,
			...sizeOf(n, known),
		}))
		for (const [id, p] of settleContainer(rects)) {
			sets.set(id, { ...sets.get(id), y: p.y })
			const rect = rects.find((r) => r.id === id)
			if (rect) {
				rect.y = p.y
			}
		}
		if (!container || !isPlaced(container) || rects.length === 0) {
			continue
		}
		const content = {
			w: Math.max(...rects.map((r) => r.x + r.w)),
			h: Math.max(...rects.map((r) => r.y + r.h)),
		}
		const current = sizeOf(container)
		const size = fitFrame(current, content)
		if (size.w !== current.w || size.h !== current.h) {
			known.set(container.id, size)
			sets.set(container.id, { ...sets.get(container.id), ...size })
		}
	}

	return [...sets].map(([id, set]): Op => ({ op: "update", id, set }))
}

export type Placement = {
	positions: Map<string, Point>
	sizes: Map<string, Size> // to write: fitted frames and measured unplaced nodes
	unplaced: string[]
}

// Positions for every unplaced node, container by container: deeper frames first, so a frame
// has its size before its parent container lays it out. Placed nodes never move. `measured`
// gives rendered sizes of unplaced cards and images; without it the layout uses defaults and
// serves only as a provisional position for rendering them before measurement. Measured sizes
// of placed nodes shape the layout but are written by the measured-size pack, not here.
export function planPlacement(
	board: Board,
	measured: Map<string, Size> = new Map(),
): Placement {
	const nodes = nodeMap(board)
	const positions = new Map<string, Point>()
	const unplacedNodes = board.nodes.filter((n) => !isPlaced(n))
	const unplaced = unplacedNodes.map((n) => n.id)
	const sizes = new Map<string, Size>()
	for (const node of unplacedNodes) {
		const size = measured.get(node.id)
		if (size && node.type !== "frame") {
			sizes.set(node.id, size)
		}
	}
	// Layout sees the rendered size of every card and image, placed obstacles included: a placed
	// card that grew since its size was stored must not be covered by the new block.
	const known = new Map(
		[...measured].filter(([id]) => nodes.get(id)?.type !== "frame"),
	)
	const frames = board.nodes.filter((n): n is FrameNode => n.type === "frame")
	frames.sort((a, b) => depthOf(nodes, b.id) - depthOf(nodes, a.id))

	for (const container of [...frames, undefined]) {
		const children = board.nodes.filter((n) => n.parentId === container?.id)
		const open = children.filter((n) => !isPlaced(n))
		const needsLayout =
			open.length > 0 || (container !== undefined && !isPlaced(container))
		if (!needsLayout) {
			continue
		}
		const result = placeContainer(
			board,
			container,
			open,
			children.filter(isPlaced),
			known,
		)
		for (const [id, p] of result.positions) {
			positions.set(id, p)
		}
		if (container && result.frameSize) {
			known.set(container.id, result.frameSize)
			sizes.set(container.id, result.frameSize)
		}
	}

	return { positions, sizes, unplaced }
}

// Lays out all direct children of a frame again, as if none were placed.
export function arrangeFrame(board: Board, frame: FrameNode): ContainerLayout {
	const children = board.nodes.filter((n) => n.parentId === frame.id)

	return placeContainer(board, frame, children, [], new Map())
}

// One service pack for a placement: positions of unplaced nodes, their measured sizes and the
// sizes of fitted frames.
export function placementOps(placement: Placement): Op[] {
	const sets = new Map<string, Record<string, unknown>>()
	for (const id of placement.unplaced) {
		const p = placement.positions.get(id)
		if (p) {
			sets.set(id, { x: p.x, y: p.y })
		}
	}
	for (const [id, size] of placement.sizes) {
		sets.set(id, { ...sets.get(id), w: size.w, h: size.h })
	}
	const ops = [...sets].map(([id, set]): Op => ({ op: "update", id, set }))

	return ops
}
