import type { PeerBoard, PeerBoardNode } from "../types"

// SVG_LIMIT is the most characters the Svg element takes.
const SVG_LIMIT = 131_072
// The schematic is drawn 1:1 up to WIDTH px, then scaled down to it.
const WIDTH = 560
const PAD = 12
const GAP = 16
const FONT = 11
const LINE = 14
const HEADER = 22
const CARD_W = 160
const CARD_PAD = 8
const MAX_LINES = 4
// An average sans-serif glyph is about 0.6 em wide.
const GLYPH = FONT * 0.6
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

	return fit(first, 40)
}

// fit cuts text to n glyphs, ending a cut line with "…". Glyphs are code
// points, so a cut never splits a surrogate pair.
function fit(text: string, n: number) {
	const glyphs = Array.from(text)

	return glyphs.length > n
		? `${glyphs.slice(0, Math.max(0, n - 1)).join("")}…`
		: text
}

// wrap breaks a card's text into lines of at most n glyphs, by words, and
// keeps MAX_LINES of them; a cut ends with "…".
function wrap(text: string, n: number) {
	const lines: string[][] = []
	for (const paragraph of text.split("\n")) {
		let line: string[] = []
		for (const word of paragraph.split(/\s+/).filter(Boolean)) {
			let rest = Array.from(word)
			while (rest.length > 0) {
				const room = line.length > 0 ? n - line.length - 1 : n
				if (rest.length <= room) {
					line = line.length > 0 ? [...line, " ", ...rest] : rest
					rest = []
				} else if (line.length > 0) {
					lines.push(line)
					line = []
				} else {
					lines.push(rest.slice(0, n))
					rest = rest.slice(n)
				}
			}
		}
		lines.push(line)
	}
	while (lines.length > 1 && lines.at(-1)?.length === 0) lines.pop()
	const kept = lines.slice(0, MAX_LINES)
	if (lines.length > MAX_LINES) {
		kept[MAX_LINES - 1] = [...(kept[MAX_LINES - 1] ?? []).slice(0, n - 1), "…"]
	}

	return kept.map((line) => line.join(""))
}

// ranks gives each item its layer: the longest path to it along the
// links. A link that closes a cycle is skipped, so a cycle still ends.
function ranks(ids: string[], links: [string, string][]) {
	const out = new Map(ids.map((id) => [id, [] as string[]]))
	for (const [from, to] of links) {
		if (from !== to) out.get(from)?.push(to)
	}
	const state = new Map<string, "open" | "done">()
	const kept = new Map(ids.map((id) => [id, [] as string[]]))
	const order: string[] = []
	const visit = (id: string) => {
		state.set(id, "open")
		for (const to of out.get(id) ?? []) {
			if (state.get(to) === "open") continue
			kept.get(id)?.push(to)
			if (!state.has(to)) visit(to)
		}
		state.set(id, "done")
		order.push(id)
	}
	for (const id of ids) if (!state.has(id)) visit(id)

	const rank = new Map(ids.map((id) => [id, 0]))
	for (const id of order.reverse()) {
		for (const to of kept.get(id) ?? []) {
			rank.set(to, Math.max(rank.get(to) ?? 0, (rank.get(id) ?? 0) + 1))
		}
	}

	return rank
}

// schematic lays the board out without its stored geometry, so a board
// nobody opened in the browser still has a picture: each frame holds its
// children in layers along the edges between them, a layer wraps when it
// is wider than the drawing, and the top frames go by order.
function schematic(board: PeerBoard) {
	const byId = new Map(board.nodes.map((node) => [node.id, node]))
	const parentOf = (node: PeerBoardNode) =>
		node.parentId && byId.get(node.parentId)?.type === "frame"
			? node.parentId
			: ""
	const kids = new Map<string, PeerBoardNode[]>()
	for (const node of board.nodes) {
		const list = kids.get(parentOf(node)) ?? []
		list.push(node)
		kids.set(parentOf(node), list)
	}
	// lift gives the ancestor of id that is a direct child of container.
	const lift = (id: string, container: string) => {
		const seen = new Set<string>()
		for (let node = byId.get(id); node && !seen.has(node.id); ) {
			seen.add(node.id)
			const parent = parentOf(node)
			if (parent === container) return node.id
			if (parent === "") return null
			node = byId.get(parent)
		}
		return null
	}
	const lines = new Map<string, string[]>()
	const size = new Map<string, { w: number; h: number }>()
	const rel = new Map<string, { x: number; y: number }>()
	const arrange = (container: string, depth: number) => {
		const own = (kids.get(container) ?? []).filter((node) => !size.has(node.id))
		const items = [
			...own
				.filter((node) => node.type === "frame")
				.sort((a, b) => (a.order ?? 0) - (b.order ?? 0)),
			...own.filter((node) => node.type !== "frame"),
		]
		for (const node of items) {
			if (node.type === "frame") {
				size.set(node.id, { w: 0, h: 0 })
				const inner = arrange(node.id, depth + 1)
				size.set(node.id, {
					w: Math.max(inner.w + 2 * PAD, CARD_W),
					h: HEADER + inner.h + PAD,
				})
			} else {
				const text =
					node.type === "image" ? node.caption || "image" : (node.text ?? "")
				const wrapped = wrap(text, Math.floor((CARD_W - 2 * CARD_PAD) / GLYPH))
				lines.set(node.id, wrapped)
				size.set(node.id, {
					w: CARD_W,
					h: wrapped.length * LINE + 2 * CARD_PAD,
				})
			}
		}
		const ids = items.map((node) => node.id)
		const links: [string, string][] = []
		for (const edge of board.edges) {
			const from = lift(edge.from, container)
			const to = lift(edge.to, container)
			if (from && to) links.push([from, to])
		}
		const rank = ranks(ids, links)
		const layers: string[][] = []
		for (const id of ids) {
			const r = rank.get(id) ?? 0
			layers[r] = [...(layers[r] ?? []), id]
		}
		const row = Math.max(CARD_W, WIDTH - 2 * PAD * (depth + 1))
		const left = container ? PAD : 0
		const top = container ? HEADER : 0
		let y = 0
		let w = 0
		for (const layer of layers.filter(Boolean)) {
			let x = 0
			let tall = 0
			for (const id of layer) {
				const s = size.get(id) ?? { w: 0, h: 0 }
				if (x > 0 && x + s.w > row) {
					y += tall + GAP
					x = 0
					tall = 0
				}
				rel.set(id, { x: left + x, y: top + y })
				x += s.w + GAP
				tall = Math.max(tall, s.h)
				w = Math.max(w, x - GAP)
			}
			y += tall + GAP
		}

		return { w, h: Math.max(0, y - GAP) }
	}
	const whole = arrange("", 0)
	const boxes = new Map<string, Box>()
	const place = (node: PeerBoardNode): Box | null => {
		const known = boxes.get(node.id)
		if (known) return known
		const at = rel.get(node.id)
		const s = size.get(node.id)
		if (!at || !s) return null
		const parent = parentOf(node)
		const origin = parent ? place(byId.get(parent) as PeerBoardNode) : null
		const box = {
			x: (origin?.x ?? 0) + at.x,
			y: (origin?.y ?? 0) + at.y,
			w: s.w,
			h: s.h,
		}
		boxes.set(node.id, box)

		return box
	}
	for (const node of board.nodes) place(node)

	return { boxes, lines, whole }
}

// border is where the line from box b's center toward point p leaves b.
function border(b: Box, p: { x: number; y: number }) {
	const cx = b.x + b.w / 2
	const cy = b.y + b.h / 2
	const dx = p.x - cx
	const dy = p.y - cy
	const t = Math.min(
		dx ? b.w / 2 / Math.abs(dx) : Number.POSITIVE_INFINITY,
		dy ? b.h / 2 / Math.abs(dy) : Number.POSITIVE_INFINITY,
		1,
	)

	return { x: cx + dx * t, y: cy + dy * t }
}

const round = (n: number) => Math.round(n * 10) / 10

// inside tells whether box a lies within box b.
const inside = (a: Box, b: Box) =>
	a.x >= b.x && a.y >= b.y && a.x + a.w <= b.x + b.w && a.y + a.h <= b.y + b.h

// boardSvg draws the board's schematic: frames as titled boxes, cards and
// images with their wrapped text clipped to the box, and edges as arrows
// between the boxes' borders; a loop is an arc on its box. The stored positions are not used. A
// drawing over the Svg limit keeps the frames only; null for an empty
// board or when even the frames do not fit.
export function boardSvg(board: PeerBoard): string | null {
	if (board.nodes.length === 0) return null

	const { boxes, lines, whole } = schematic(board)
	// A loop's arc and label rise above its box; on the top row they need
	// room over the drawing.
	const rise = board.edges.some((edge) => edge.from === edge.to) ? 10 : 0
	const width = whole.w + 2 * PAD
	const height = whole.h + 2 * PAD + rise
	const shownWidth = Math.min(WIDTH, width)
	const shownHeight = Math.max(40, Math.round((shownWidth * height) / width))
	const svg = (body: string) =>
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="${-PAD} ${-PAD - rise} ${width} ${height}" width="${shownWidth}" height="${shownHeight}" font-family="sans-serif" font-size="${FONT}"><defs><marker id="arrow" viewBox="0 0 8 8" refX="8" refY="4" markerWidth="6" markerHeight="6" orient="auto"><path d="M0,0L8,4L0,8z" fill="#888"/></marker></defs>${body}</svg>`
	const frame = (node: PeerBoardNode, i: number) => {
		const b = boxes.get(node.id) as Box
		const title = fit(label(node), Math.floor((b.w - 2 * PAD) / GLYPH))

		// A title of wide glyphs is clipped to the frame like a card's text.
		return `<rect x="${b.x}" y="${b.y}" width="${b.w}" height="${b.h}" rx="6" fill="#8881" stroke="#888"/><clipPath id="f${i}"><rect x="${b.x}" y="${b.y}" width="${b.w - PAD / 2}" height="${HEADER}"/></clipPath><text x="${b.x + PAD}" y="${b.y + 15}" clip-path="url(#f${i})" fill="#888">${esc(title)}</text>`
	}
	const card = (node: PeerBoardNode, i: number) => {
		const b = boxes.get(node.id) as Box
		const fill = CARD_FILL[node.color ?? "gray"] ?? "#8883"
		const dash = node.type === "image" ? ' stroke-dasharray="4 3"' : ""
		const text = (lines.get(node.id) ?? [])
			.map(
				(line, n) =>
					`<tspan x="${b.x + CARD_PAD}" y="${b.y + CARD_PAD + 11 + n * LINE}">${esc(line)}</tspan>`,
			)
			.join("")

		return `<rect x="${b.x}" y="${b.y}" width="${b.w}" height="${b.h}" rx="6" fill="${fill}" stroke="#888"${dash}/><clipPath id="c${i}"><rect x="${b.x}" y="${b.y}" width="${b.w}" height="${b.h}"/></clipPath><text clip-path="url(#c${i})" fill="#888">${text}</text>`
	}
	const shown = board.nodes.filter((node) => boxes.has(node.id))
	const frames = shown.filter((node) => node.type === "frame").map(frame)
	const cards = shown.filter((node) => node.type !== "frame").map(card)
	const edges = board.edges.map((edge) => {
		const a = boxes.get(edge.from)
		const b = boxes.get(edge.to)
		if (!a || !b) return ""
		if (edge.from === edge.to) {
			// A loop is a small arc over the box's top right corner.
			const x = a.x + a.w
			const loop = `<path d="M${x - 24},${a.y} C${x - 24},${a.y - 18} ${x + 18},${a.y - 18} ${x},${a.y + 14}" fill="none" stroke="#888" marker-end="url(#arrow)"/>`
			const tag = edge.label
				? `<text x="${x - 28}" y="${a.y - 4}" text-anchor="end" font-size="10" fill="#888">${esc(fit(edge.label, 24))}</text>`
				: ""

			return loop + tag
		}
		let from = border(a, { x: b.x + b.w / 2, y: b.y + b.h / 2 })
		let to = border(b, { x: a.x + a.w / 2, y: a.y + a.h / 2 })
		// A frame and a node inside it: the arrow runs between the frame's
		// title bar and the node's top.
		if (inside(b, a)) {
			from = { x: b.x + b.w / 2, y: a.y + HEADER - 4 }
			to = { x: b.x + b.w / 2, y: b.y }
		} else if (inside(a, b)) {
			from = { x: a.x + a.w / 2, y: a.y }
			to = { x: a.x + a.w / 2, y: b.y + HEADER - 4 }
		}
		const line = `<line x1="${round(from.x)}" y1="${round(from.y)}" x2="${round(to.x)}" y2="${round(to.y)}" stroke="#888" marker-end="url(#arrow)"/>`
		const tag = edge.label
			? `<text x="${round((from.x + to.x) / 2)}" y="${round((from.y + to.y) / 2 - 3)}" text-anchor="middle" font-size="10" fill="#888">${esc(fit(edge.label, 24))}</text>`
			: ""

		return line + tag
	})
	const full = svg([...frames, ...edges, ...cards].join(""))
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
