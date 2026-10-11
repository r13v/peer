import type { PeerBoard, PeerBoardNode } from "../types"

// SVG_LIMIT is the most characters the Svg element takes.
const SVG_LIMIT = 131_072
// The schematic is drawn 1:1 up to WIDTH px, then scaled down to it.
const WIDTH = 720
const PAD = 12
const GAP = 16
// LABEL_GAP parts two layers that a labeled edge joins, so the label sits
// between them and not on a box.
const LABEL_GAP = 30
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
	const open = openThreads(board)
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
				// A labeled loop on a card in the frame gets room on the right.
				const loops = board.edges
					.filter(
						(edge) =>
							edge.label &&
							edge.from === edge.to &&
							byId.get(edge.from)?.type !== "frame" &&
							parentOf(byId.get(edge.from) as PeerBoardNode) === node.id,
					)
					.map(
						(edge) =>
							14 + Math.min(40, Array.from(edge.label ?? "").length) * 6,
					)
				size.set(node.id, {
					w: Math.max(inner.w + 2 * PAD + Math.max(0, ...loops), CARD_W),
					h: HEADER + inner.h + PAD,
				})
			} else {
				const text =
					node.type === "image" ? node.caption || "image" : (node.text ?? "")
				const wrapped = wrap(text, Math.floor((CARD_W - 2 * CARD_PAD) / GLYPH))
				lines.set(node.id, wrapped)
				// A card with open threads keeps a row under its text for the badge.
				const badge = open.has(node.id) ? LINE : 0
				size.set(node.id, {
					w: CARD_W,
					h: wrapped.length * LINE + 2 * CARD_PAD + badge,
				})
			}
		}
		const ids = items.map((node) => node.id)
		const links: [string, string][] = []
		const labeled: [string, string][] = []
		for (const edge of board.edges) {
			const from = lift(edge.from, container)
			const to = lift(edge.to, container)
			if (from && to) links.push([from, to])
			if (from && to && edge.label) labeled.push([from, to])
		}
		const rank = ranks(ids, links)
		const roomy = new Set(
			labeled
				.filter(([a, b]) => rank.get(a) !== rank.get(b))
				.map(([a, b]) => Math.min(rank.get(a) ?? 0, rank.get(b) ?? 0)),
		)
		const layers: string[][] = []
		for (const id of ids) {
			const r = rank.get(id) ?? 0
			layers[r] = [...(layers[r] ?? []), id]
		}
		const row = Math.max(CARD_W, WIDTH - 2 * PAD * (depth + 1))
		// A labeled edge between a frame and its own content gets a row under
		// the title bar for its label.
		const extra =
			container &&
			board.edges.some(
				(edge) =>
					edge.label &&
					((edge.from === container && lift(edge.to, container)) ||
						(edge.to === container && lift(edge.from, container))),
			)
				? 14
				: 0
		const left = container ? PAD : 0
		const top = container ? HEADER + extra : 0
		let y = 0
		let w = 0
		for (const [r, layer] of layers.entries()) {
			if (!layer) continue
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
			y += tall + (roomy.has(r) ? LABEL_GAP : GAP)
		}
		y -= roomy.has(layers.length - 1) ? LABEL_GAP : GAP

		return { w, h: Math.max(0, y) + extra }
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

	return { boxes, lines, whole, open }
}

type Point = { x: number; y: number }

// openThreads counts the open threads on each frame, node and edge by its
// id; a thread counts on its own target only.
function openThreads(board: PeerBoard) {
	const open = new Map<string, number>()
	for (const thread of board.threads) {
		const id = thread.target.id
		if (!thread.resolved && id) open.set(id, (open.get(id) ?? 0) + 1)
	}

	return open
}

const center = (b: Box): Point => ({ x: b.x + b.w / 2, y: b.y + b.h / 2 })

// reach is the step along d from at to the side of [lo, hi] it heads to.
function reach(d: number, lo: number, hi: number, at: number) {
	if (d > 0) return (hi - at) / d
	if (d < 0) return (lo - at) / d

	return Number.POSITIVE_INFINITY
}

// exit is where the line from o, a point in box b, toward p leaves b.
function exit(b: Box, o: Point, p: Point) {
	const dx = p.x - o.x
	const dy = p.y - o.y
	const t = Math.min(
		reach(dx, b.x, b.x + b.w, o.x),
		reach(dy, b.y, b.y + b.h, o.y),
		1,
	)

	return { x: o.x + dx * t, y: o.y + dy * t }
}

// crosses tells whether the segment from p to q passes through the inside
// of box b (Liang–Barsky clipping on b shrunk by one unit).
function crosses(p: Point, q: Point, b: Box) {
	let lo = 0
	let hi = 1
	const dx = q.x - p.x
	const dy = q.y - p.y
	const sides: [number, number][] = [
		[-dx, p.x - (b.x + 1)],
		[dx, b.x + b.w - 1 - p.x],
		[-dy, p.y - (b.y + 1)],
		[dy, b.y + b.h - 1 - p.y],
	]
	for (const [n, d] of sides) {
		if (n === 0) {
			if (d < 0) return false
		} else if (n < 0) {
			lo = Math.max(lo, d / n)
		} else {
			hi = Math.min(hi, d / n)
		}
	}

	return lo < hi
}

const round = (n: number) => Math.round(n * 10) / 10

// overlaps tells whether boxes a and b share some area.
const overlaps = (a: Box, b: Box) =>
	a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h

// inside tells whether box a lies within box b.
const inside = (a: Box, b: Box) =>
	a.x >= b.x && a.y >= b.y && a.x + a.w <= b.x + b.w && a.y + a.h <= b.y + b.h

// boardSvg draws the board's schematic: frames as titled boxes, cards and
// images with their wrapped text clipped to the box, and edges as arrows
// between the boxes' borders. Edges between one pair of boxes are drawn
// side by side; an edge that would cross another card goes around it on
// the right; a loop is an arc on its box. Each box and edge has a <title>
// with its full text, and an open thread a "💬 N" badge. The stored
// positions are not used. A drawing over the Svg limit keeps the frames
// only; null for an empty board or when even the frames do not fit.
export function boardSvg(board: PeerBoard): string | null {
	if (board.nodes.length === 0) return null

	const { boxes, lines, whole, open } = schematic(board)
	const badge = (id: string) => (open.has(id) ? `💬 ${open.get(id)}` : "")
	const frame = (node: PeerBoardNode, i: number) => {
		const b = boxes.get(node.id) as Box
		const mark = badge(node.id)
		// The badge has a fixed slot at the right of the title bar; the title
		// is clipped before it.
		const slot = b.x + b.w - PAD
		const room = mark ? slot - 40 - (b.x + PAD) : b.w - 2 * PAD
		const title = fit(label(node), Math.floor(room / GLYPH))
		const count = mark
			? `<text x="${slot}" y="${b.y + 15}" text-anchor="end" font-size="10" fill="#888">${mark}</text>`
			: ""

		// A title of wide glyphs is clipped to the frame like a card's text.
		return `<g><title>${esc(node.title ?? "")}</title><rect x="${b.x}" y="${b.y}" width="${b.w}" height="${b.h}" rx="6" fill="#8881" stroke="#888"/><clipPath id="f${i}"><rect x="${b.x}" y="${b.y}" width="${b.x + PAD + room - b.x}" height="${HEADER}"/></clipPath><text x="${b.x + PAD}" y="${b.y + 15}" clip-path="url(#f${i})" font-weight="600" fill="#888">${esc(title)}</text>${count}</g>`
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
		const full = node.type === "image" ? node.caption : node.text
		const mark = badge(node.id)
		const count = mark
			? `<text x="${b.x + b.w - CARD_PAD}" y="${b.y + b.h - CARD_PAD}" text-anchor="end" font-size="10" fill="#888">${mark}</text>`
			: ""

		return `<g><title>${esc(full || node.type)}</title><rect x="${b.x}" y="${b.y}" width="${b.w}" height="${b.h}" rx="6" fill="${fill}" stroke="#888"${dash}/><clipPath id="c${i}"><rect x="${b.x}" y="${b.y}" width="${b.w}" height="${b.h}"/></clipPath><text clip-path="url(#c${i})" fill="#888">${text}</text>${count}</g>`
	}
	const shown = board.nodes.filter((node) => boxes.has(node.id))
	const cardBoxes = shown
		.filter((node) => node.type !== "frame")
		.map((node) => [node.id, boxes.get(node.id) as Box] as const)
	const frameBoxes = shown
		.filter((node) => node.type === "frame")
		.map((node) => boxes.get(node.id) as Box)
	// Edges of one unordered pair are spread across the line between the
	// pair's centers, measured in the pair's own direction, so an edge and
	// its reverse do not draw on one line.
	const pairs = new Map<string, string[]>()
	for (const edge of board.edges) {
		const key = [edge.from, edge.to].sort().join(" ")
		pairs.set(key, [...(pairs.get(key) ?? []), edge.id])
	}
	let right = whole.w
	const tag = (x: number, y: number, text: string, anchor: string, n = 24) =>
		`<text x="${round(x)}" y="${round(y)}" text-anchor="${anchor}" font-size="10" fill="#888">${esc(fit(text, n))}</text>`
	type Spot = { x: number; y: number; anchor: "start" | "end" | "middle" }
	// put draws a label at the first of the spots where it touches no card,
	// no frame's title bar and no frame's border, cut to fit inside the
	// frame around the spot; a label with no free spot, or room for less
	// than six glyphs, stays only in its edge's <title>. A label with a home
	// box stays inside it.
	const put = (spots: Spot[], text: string, home?: Box) => {
		for (const spot of spots) {
			const around = frameBoxes
				.filter((f) => inside({ ...spot, w: 0, h: 0 }, f))
				.sort((f, g) => f.w * f.h - g.w * g.h)[0]
			const lo = around ? around.x + 4 : Number.NEGATIVE_INFINITY
			const hi = around ? around.x + around.w - 4 : Number.POSITIVE_INFINITY
			const room = {
				start: hi - spot.x,
				end: spot.x - lo,
				middle: 2 * Math.min(hi - spot.x, spot.x - lo),
			}[spot.anchor]
			const cut = fit(text, Math.min(40, Math.floor(room / 6)))
			const glyphs = Array.from(cut).length
			if (glyphs < Math.min(6, Array.from(text).length)) continue
			const w = glyphs * 6
			const shift = { start: 0, end: w, middle: w / 2 }[spot.anchor]
			const r = { x: spot.x - shift, y: spot.y - 9, w, h: 11 }
			const free =
				(!home || inside(r, home)) &&
				cardBoxes.every(([, box]) => !overlaps(r, box)) &&
				frameBoxes.every((f) =>
					inside(r, f)
						? !overlaps(r, { x: f.x, y: f.y, w: f.w, h: HEADER })
						: !overlaps(r, f),
				)
			if (free) {
				right = Math.max(right, r.x + w)
				return tag(spot.x, spot.y, cut, spot.anchor, 40)
			}
		}

		return ""
	}
	const byId = new Map(board.edges.map((edge) => [edge.id, edge]))
	// heading is an arrow for the way an edge runs on the drawing.
	const heading = (from: Box, to: Box) => {
		if (inside(to, from)) return "↓"
		if (inside(from, to)) return "↑"
		const dx = center(to).x - center(from).x
		const dy = center(to).y - center(from).y
		if (Math.abs(dx) > Math.abs(dy)) return dx > 0 ? "→" : "←"

		return dy > 0 ? "↓" : "↑"
	}
	// groupLabel is one label for all the edges of a pair, each with the
	// arrow of its way when there are several, so no two labels overlap.
	const groupLabel = (group: string[]) => {
		const labeled = group
			.map((id) => byId.get(id))
			.filter((edge) => edge?.label)
		if (labeled.length === 0) return ""
		if (group.length === 1) return labeled[0]?.label ?? ""

		return labeled
			.map(
				(edge) =>
					`${heading(boxes.get(edge?.from ?? "") as Box, boxes.get(edge?.to ?? "") as Box)} ${edge?.label}`,
			)
			.join(" · ")
	}
	const arrow = (points: Point[]) =>
		points.length === 2
			? `<line x1="${round(points[0]?.x ?? 0)}" y1="${round(points[0]?.y ?? 0)}" x2="${round(points[1]?.x ?? 0)}" y2="${round(points[1]?.y ?? 0)}" stroke="#888" marker-end="url(#arrow)"/>`
			: `<polyline points="${points.map((p) => `${round(p.x)},${round(p.y)}`).join(" ")}" fill="none" stroke="#888" marker-end="url(#arrow)"/>`
	const edges = board.edges.map((edge) => {
		const a = boxes.get(edge.from)
		const b = boxes.get(edge.to)
		if (!a || !b) return ""
		const title = edge.label ? `<title>${esc(edge.label)}</title>` : ""
		if (edge.from === edge.to) {
			// A loop is a small arc low on the box's right side, inside the gap
			// to the next box or the frame's border; a detour enters higher.
			const x = a.x + a.w
			const y = a.y + a.h - 12
			const loop = `<path d="M${x},${y - 7} C${x + 13},${y - 9} ${x + 13},${y + 9} ${x},${y + 7}" fill="none" stroke="#888" marker-end="url(#arrow)"/>`
			const home = frameBoxes
				.filter((f) => f !== a && inside(a, f))
				.sort((f, g) => f.w * f.h - g.w * g.h)[0]
			const text = edge.label
				? put([{ x: x + 14, y: y + 3, anchor: "start" }], edge.label, home)
				: ""

			return `<g>${title}${loop}${text}</g>`
		}
		const [first, second] = [edge.from, edge.to].sort()
		const group = pairs.get([first, second].join(" ")) ?? []
		const index = group.indexOf(edge.id)
		// Edges of a pair are spread 6 units apart, inside the smaller box,
		// so many edges of one pair still start and end on their boxes.
		const limit = Math.max(0, Math.min(a.w, a.h, b.w, b.h) / 2 - 4)
		const spread = (k: number) =>
			Math.max(-limit, Math.min(limit, (k - (group.length - 1) / 2) * 6))
		const shift = spread(index)
		const half = spread(group.length - 1)
		// Only the pair's first edge draws the pair's label.
		const label = index === 0 ? groupLabel(group) : ""
		// A frame and a node inside it: the arrow runs between the frame's
		// title bar and the node's top.
		if (inside(b, a) || inside(a, b)) {
			const inner = inside(b, a) ? b : a
			const outer = inner === b ? a : b
			const x = inner.x + inner.w / 2 + shift
			const bar = { x, y: outer.y + HEADER - 2 }
			const top = { x, y: inner.y }
			const points = inner === b ? [bar, top] : [top, bar]
			const y = (bar.y + top.y) / 2 + 3
			const middle = inner.x + inner.w / 2
			const text = label
				? put(
						[
							{ x: middle + half + 4, y, anchor: "start" },
							{ x: middle - half - 4, y, anchor: "end" },
						],
						label,
					)
				: ""

			return `<g>${title}${arrow(points)}${text}</g>`
		}
		const p = center(boxes.get(first ?? "") as Box)
		const q = center(boxes.get(second ?? "") as Box)
		const length = Math.hypot(q.x - p.x, q.y - p.y) || 1
		const normal = { x: -(q.y - p.y) / length, y: (q.x - p.x) / length }
		const ca = {
			x: center(a).x + normal.x * shift,
			y: center(a).y + normal.y * shift,
		}
		const cb = {
			x: center(b).x + normal.x * shift,
			y: center(b).y + normal.y * shift,
		}
		const from = exit(a, ca, cb)
		const to = exit(b, cb, ca)
		const others = cardBoxes.filter(
			([id]) => id !== edge.from && id !== edge.to,
		)
		const blocked = others.filter(([, box]) => crosses(from, to, box))
		if (blocked.length > 0) {
			// Around on the right: out of a's right side, down a lane right of
			// everything in the way and of the frames around it, into b's
			// right side. Each edge of the pair has its own lane and rows.
			const near = [a, b, ...blocked.map(([, box]) => box)]
			const lane =
				Math.max(
					...near.map((box) => box.x + box.w),
					...frameBoxes
						.filter((f) => near.some((box) => inside(box, f)))
						.map((f) => f.x + f.w),
				) +
				10 +
				index * 6
			// It leaves and enters near the boxes' tops, clear of a loop low
			// on their right side.
			const ya = a.y + 5 + Math.min(index * 6, a.h / 2 - 8)
			const yb = b.y + 5 + Math.min(index * 6, b.h / 2 - 8)
			const points = [
				{ x: a.x + a.w, y: ya },
				{ x: lane, y: ya },
				{ x: lane, y: yb },
				{ x: b.x + b.w, y: yb },
			]
			const clear = points
				.slice(1)
				.every((pt, n) =>
					others.every(([, box]) => !crosses(points[n] as Point, pt, box)),
				)
			if (clear) {
				right = Math.max(right, lane + 4)
				const at = lane + (group.length - 1 - index) * 6 + 4
				const y = (center(a).y + center(b).y) / 2
				const text = label ? put([{ x: at, y, anchor: "start" }], label) : ""

				return `<g>${title}${arrow(points)}${text}</g>`
			}
		}
		// The pair's label sits beside the bundle of its lines: right of a
		// steep one, over a flat one.
		const base = {
			x:
				(exit(a, center(a), center(b)).x + exit(b, center(b), center(a)).x) / 2,
			y:
				(exit(a, center(a), center(b)).y + exit(b, center(b), center(a)).y) / 2,
		}
		const steep = Math.abs(q.y - p.y) >= Math.abs(q.x - p.x)
		const spots: Spot[] = steep
			? [
					{ x: base.x + half + 4, y: base.y + 3, anchor: "start" },
					{ x: base.x - half - 4, y: base.y + 3, anchor: "end" },
				]
			: [
					{ x: base.x, y: base.y - half - 4, anchor: "middle" },
					{ x: base.x, y: base.y + half + 12, anchor: "middle" },
				]
		const text = label ? put(spots, label) : ""

		return `<g>${title}${arrow([from, to])}${text}</g>`
	})
	const width = right + 2 * PAD
	const height = whole.h + 2 * PAD
	const shownWidth = Math.min(WIDTH, width)
	const shownHeight = Math.max(40, Math.round((shownWidth * height) / width))
	const svg = (body: string) =>
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="${-PAD} ${-PAD} ${width} ${height}" width="${shownWidth}" height="${shownHeight}" font-family="sans-serif" font-size="${FONT}"><defs><marker id="arrow" viewBox="0 0 8 8" refX="8" refY="4" markerWidth="6" markerHeight="6" orient="auto"><path d="M0,0L8,4L0,8z" fill="#888"/></marker></defs>${body}</svg>`
	const frames = shown.filter((node) => node.type === "frame").map(frame)
	const cards = shown.filter((node) => node.type !== "frame").map(card)
	const full = svg([...frames, ...edges, ...cards].join(""))
	if (full.length <= SVG_LIMIT) return full

	const framesOnly = svg(frames.join(""))

	return framesOnly.length <= SVG_LIMIT ? framesOnly : null
}

// boardStats counts a board's frames, its cards and images, and its open
// threads, for the line under the board's title.
export function boardStats(board: PeerBoard) {
	const frames = board.nodes.filter((node) => node.type === "frame").length
	const nodes = board.nodes.length - frames
	const threads = board.threads.filter((thread) => !thread.resolved).length

	return `${plural(frames, "frame")} · ${plural(nodes, "node")} · ${plural(threads, "open thread")}`
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
