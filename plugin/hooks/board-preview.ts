import type { PeerBoard, PeerBoardNode } from "../types"

// SVG_LIMIT is the most characters the Svg element takes.
const SVG_LIMIT = 131_072
const WIDTH = 560
const PAD = 20
// Sizes of a node the browser has not measured yet, in board units.
const CARD = { w: 240, h: 72 }
const FRAME = { w: 640, h: 360 }
const CARD_FILL: Record<string, string> = {
	gray: "#8883",
	yellow: "#e5c07b66",
	green: "#98c37966",
	blue: "#4c8dff55",
	purple: "#b48cff55",
	red: "#e0606066",
}

type Box = { x: number; y: number; w: number; h: number }

// label is a node's first line: a card's text, an image's caption or a
// frame's title.
function label(node: PeerBoardNode) {
	const text = node.text ?? node.caption ?? node.title ?? ""
	const first = text.split("\n")[0]?.trim() || node.type

	return first.length > 40 ? `${first.slice(0, 39)}…` : first
}

// boxes gives each placed node its box on the board: a child's x and y
// count from its frame's corner, so the frames' offsets are added. A node
// without x and y, or inside a frame without them, is not placed.
function boxes(board: PeerBoard) {
	const byId = new Map(board.nodes.map((node) => [node.id, node]))
	const placed = new Map<string, Box | null>()
	const place = (node: PeerBoardNode, depth: number): Box | null => {
		const known = placed.get(node.id)
		if (known !== undefined) return known
		if (node.x === undefined || node.y === undefined || depth > 64) return null

		const parent = node.parentId ? byId.get(node.parentId) : undefined
		const origin = parent ? place(parent, depth + 1) : { x: 0, y: 0 }
		const size = node.type === "frame" ? FRAME : CARD
		const box = origin && {
			x: origin.x + node.x,
			y: origin.y + node.y,
			w: node.w ?? size.w,
			h: node.h ?? size.h,
		}
		placed.set(node.id, box)

		return box
	}
	for (const node of board.nodes) place(node, 0)

	return placed
}

// unplaced lists the labels of the nodes the browser has not placed yet.
export function unplaced(board: PeerBoard) {
	const placed = boxes(board)

	return board.nodes
		.filter((node) => !placed.get(node.id))
		.map((node) => label(node))
}

// boardSvg draws the placed frames and nodes as rectangles with their
// labels, and the edges as lines between node centers. A drawing over the
// Svg limit keeps the frames only; null when even they do not fit.
export function boardSvg(board: PeerBoard): string | null {
	const placed = boxes(board)
	const shown = board.nodes.filter((node) => placed.get(node.id))
	if (shown.length === 0) return null

	const all = shown.map((node) => placed.get(node.id) as Box)
	const left = Math.min(...all.map((b) => b.x)) - PAD
	const top = Math.min(...all.map((b) => b.y)) - PAD
	const width = Math.max(...all.map((b) => b.x + b.w)) - left + PAD
	const height = Math.max(...all.map((b) => b.y + b.h)) - top + PAD
	// Text keeps about 11 px at the drawn width, whatever the board's size.
	const font = (11 * width) / WIDTH
	const shownHeight = Math.max(40, Math.round((WIDTH * height) / width))
	const svg = (body: string) =>
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="${left} ${top} ${width} ${height}" width="${WIDTH}" height="${shownHeight}" font-family="sans-serif" font-size="${font.toFixed(1)}">${body}</svg>`
	const rect = (node: PeerBoardNode) => {
		const b = placed.get(node.id) as Box
		const isFrame = node.type === "frame"
		const fill = isFrame
			? "#8881"
			: (CARD_FILL[node.color ?? "gray"] ?? "#8883")
		const y = isFrame ? b.y + font * 1.3 : b.y + b.h / 2 + font / 3

		return `<rect x="${b.x}" y="${b.y}" width="${b.w}" height="${b.h}" rx="${font / 2}" fill="${fill}" stroke="#888"/><text x="${b.x + font / 2}" y="${y}" fill="#888">${esc(label(node))}</text>`
	}
	const frames = shown.filter((node) => node.type === "frame").map(rect)
	const nodes = shown.filter((node) => node.type !== "frame").map(rect)
	const center = (id: string) => {
		const b = placed.get(id)
		return b ? { x: b.x + b.w / 2, y: b.y + b.h / 2 } : null
	}
	const edges = board.edges.map((edge) => {
		const from = center(edge.from)
		const to = center(edge.to)
		return from && to
			? `<line x1="${from.x}" y1="${from.y}" x2="${to.x}" y2="${to.y}" stroke="#888"/>`
			: ""
	})
	const full = svg([...frames, ...edges, ...nodes].join(""))
	if (full.length <= SVG_LIMIT) return full

	const framesOnly = svg(frames.join(""))

	return framesOnly.length <= SVG_LIMIT ? framesOnly : null
}

// frameLines lists the frames by order, each with its count of direct
// child nodes and of open threads on the frame and those nodes, then the
// nodes outside any frame.
export function frameLines(board: PeerBoard) {
	const open = board.threads.filter((thread) => !thread.resolved)
	const frames = board.nodes
		.filter((node) => node.type === "frame")
		.sort((a, b) => (a.order ?? 0) - (b.order ?? 0))
	const lines = frames.map((frame) => {
		const children = board.nodes.filter((node) => node.parentId === frame.id)
		const ids = new Set([frame.id, ...children.map((node) => node.id)])
		const threads = open.filter(
			(thread) => thread.target.id && ids.has(thread.target.id),
		).length

		return `${label(frame)} · ${plural(children.length, "node")} · ${plural(threads, "open thread")}`
	})
	const loose = board.nodes.filter(
		(node) => node.type !== "frame" && !node.parentId,
	).length
	if (loose > 0) lines.push(`${plural(loose, "node")} outside frames`)

	return lines
}

function plural(n: number, noun: string) {
	return `${n} ${noun}${n === 1 ? "" : "s"}`
}

function esc(text: string) {
	return text.replace(/[<>&"]/g, (c) => `&#${c.charCodeAt(0)};`)
}
