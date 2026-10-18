import { expect, mock, type TestBody, test } from "claude-code/testing"
import { boardStats, boardSvg } from "../hooks/board-preview.ts"
import type { PeerBoard } from "../types"

const PANE = {
	title: "peer",
	isFocused: false,
	bodyColumns: 70,
	placement: "dock",
	scroll: { offset: 0, bodyRows: 30 },
	view: {},
} as never

const ROOM = {
	id: "csv-export",
	repo: "/repo",
	started_at: "2026-10-05T20:00:00Z",
	members: [
		{ role: "main", agent: "claude" },
		{ role: "reader", agent: "codex", pid: 1 },
	],
}

const LONG = Array.from({ length: 20 }, (_, i) => `line ${i + 1}`).join("\n")

const MESSAGES = [
	{
		id: "a",
		at: "2026-10-05T20:01:00Z",
		from: "peer",
		to: "main",
		text: "reader joined",
	},
	{
		id: "b",
		at: "2026-10-05T20:01:01Z",
		from: "peer",
		to: "main",
		text: "tester joined",
	},
	{
		id: "c",
		at: "2026-10-05T20:02:00Z",
		from: "reader",
		to: "main",
		text: "See `main.go:42` and docs/x.md",
	},
	{ id: "d", at: "2026-10-05T20:03:00Z", from: "main", to: "*", text: LONG },
]

// BOARD_LIST has one board of csv-export and one of another room only.
const BOARD_LIST = JSON.stringify({
	boards: [
		{
			key: "a1",
			path: "/repo/docs/arch.board",
			url: "http://localhost:4747/b/a1",
			title: "Architecture",
			rooms: ["csv-export"],
			revision: 1,
			mtime: "2026-10-05T20:05:00Z",
		},
		{
			key: "b2",
			path: "/store/other.board",
			url: "http://localhost:4747/b/b2",
			rooms: ["auth-fix"],
		},
	],
})

// listing is what peer board list prints; a test may change it.
const listing = { boards: BOARD_LIST }

// BOARD has frame f2 inside frame f1 and a card in each: their x and y
// count from their frame's corner. n3 is not placed yet.
const BOARD = {
	title: "Architecture",
	nodes: [
		{
			id: "f1",
			type: "frame",
			title: "Store",
			order: 1,
			x: 100,
			y: 50,
			w: 800,
			h: 500,
		},
		{
			id: "f2",
			type: "frame",
			title: "Locks",
			order: 2,
			parentId: "f1",
			x: 300,
			y: 200,
			w: 400,
			h: 250,
		},
		{
			id: "n1",
			type: "card",
			text: "board.json\nwhole state",
			parentId: "f1",
			x: 10,
			y: 20,
			w: 200,
			h: 60,
		},
		{
			id: "n2",
			type: "card",
			text: "flock on .lock",
			parentId: "f2",
			x: 30,
			y: 40,
			w: 200,
			h: 60,
		},
		{ id: "n3", type: "card", text: "Cold start", parentId: "f1" },
	],
	edges: [{ id: "e1", from: "n1", to: "n2" }],
	threads: [
		{ target: { kind: "node", id: "n1" }, resolved: false },
		{ target: { kind: "frame", id: "f2" }, resolved: true },
	],
}

const lines = (...events: unknown[]) =>
	events.map((event) => `${JSON.stringify(event)}\n`).join("")

const WATCH = lines(
	{ event: "room", room: ROOM },
	...MESSAGES.map((message) => ({ event: "message", room: ROOM.id, message })),
	{ event: "ready" },
)

// engineFooter stands for what is beneath the plugin in the SessionMode chain:
// the engine's mode labels and, when given, another plugin's button.
function engineFooter(on: Parameters<TestBody>[1], button?: string) {
	on("ui.render", { component: "SessionMode" }, ($, e) => {
		const { Box, Text, Button } = $.ui.resolve(e)
		const labels = Text({ children: e.props.modes.join(" & ") })
		return Box({
			children: button
				? [labels, Button({ key: "other", label: button, onPress: () => {} })]
				: [labels],
		} as never)
	})
}

// start runs the plugin against a checkout that tracks main.go, in UTC+3,
// with watch printing what it is given and memberlog printing memberlog,
// and records the commands it runs and the panes it opens. The session
// joins each room that watch prints unless isOwn is false, so the rooms
// are its own.
async function start(
	$: Parameters<TestBody>[0],
	on: Parameters<TestBody>[1],
	watch: string | { stderr: string } = WATCH,
	memberlog = "",
	isOwn = true,
) {
	const ran: string[][] = []
	on("process.spawn", async function* (_, e, next) {
		if (e.argv[1] !== "watch") return yield* next(e)
		ran.push([...e.argv])
		if (typeof watch !== "string") {
			yield { stream: "stderr", text: watch.stderr }
			return { value: { code: 1, signal: null } } as never
		}
		yield { stream: "stdout", text: watch }
		return { value: { code: 0, signal: null } } as never
	})
	on("process.run", (_, e) => {
		ran.push([...e.argv])
		let stdout = ""
		if (e.argv[3] === "ls-files") stdout = "main.go\n"
		else if (e.argv[0] === "date") stdout = "+0300\n"
		else if (e.argv[1] === "memberlog") stdout = memberlog
		else if (e.argv[1] === "board") stdout = listing.boards
		return { value: { exitCode: 0, stdout, stderr: "" } } as never
	})
	on("session.start", (_, e) => ({ cwd: e.cwd }) as never)
	on("session.cwd", () => ({ value: "/repo" }) as never)
	// The pane is shown from its open to its close.
	let isShown = false
	on("ui.open", (_, e) => {
		ran.push(["ui.open", e.id])
		isShown = true
		return { value: { isPlaced: true } } as never
	})
	on("ui.close", (_, e) => {
		ran.push(["ui.close", e.id])
		isShown = false
		return { value: undefined } as never
	})
	on("ui.scroll", () => ({ value: {} }) as never)
	on(
		"ui.panes",
		() =>
			({
				value: isShown
					? [{ id: "peer", title: "peer", isShown, isPlaced: true }]
					: [],
			}) as never,
	)
	// A Bash call prints the room it names, as peer join and peer start do;
	// a join as main, a reserved role, fails and prints nothing.
	on("tool.call", { tool: "Bash" }, (_, e) => {
		const command = (e as { command: string }).command
		const id = /peer \w+ ([a-z0-9-]+)/.exec(command)?.[1] ?? ROOM.id
		if (/^peer join \S+ main$/.test(command))
			return {
				result: {
					stdout: "",
					stderr: 'peer: role "main" is reserved\n',
					interrupted: false,
				},
			} as never
		return {
			result: {
				stdout: `${JSON.stringify({ ...ROOM, id })}\n`,
				stderr: "",
				interrupted: false,
			},
		} as never
	})
	await $.session.start({ source: "startup", cwd: "/repo" } as never)
	await new Promise((resolve) => setTimeout(resolve, 50))
	if (isOwn && typeof watch === "string") {
		const ids = new Set(
			[...watch.matchAll(/"event":"room","room":\{"id":"([^"]+)"/g)].map(
				(match) => match[1],
			),
		)
		for (const id of ids)
			await $.tool.call({
				tool: "Bash",
				command: `peer join ${id} reader`,
			} as never)
	}
	return ran
}

test("messages are cards with the time first, notices one line, file refs links", async ($, on) => {
	await start($, on)
	// The terminal draws the same room, without what only the desktop has.
	const terminal = await $.ui.mount({
		plugin: "peer",
		surface: "terminal",
		component: "Pane",
		requestId: "peer",
		props: PANE,
	})
	expect(JSON.stringify(await terminal.drawn())).toContain("main.go:42")
	const ui = await $.ui.mount({
		plugin: "peer",
		surface: "desktop",
		component: "Pane",
		requestId: "peer",
		props: PANE,
	})
	const drawn = JSON.stringify(await ui.drawn())
	// A reader's card: its purple mixed into the background.
	expect(drawn).toContain('"borderStyle":"single","borderColor":"#493d5f"')
	// The chat tab has no message count.
	expect(drawn).toContain('"label":"● chat"')
	// Times are in the host's zone, UTC+3 here, newest first, under the tabs.
	expect(drawn.indexOf('"label":"● chat"')).toBeLessThan(drawn.indexOf("23:03"))
	expect(drawn.indexOf("23:03")).toBeLessThan(drawn.indexOf("23:02"))
	expect(drawn).toContain("reader joined · tester joined")
	expect(drawn).toContain("[`main.go:42`](file:///repo/main.go)")
	// docs/x.md is not tracked, so it stays text.
	expect(drawn).not.toContain("file:///repo/docs/x.md")
})

test("a long message shows its start until it is expanded", async ($, on) => {
	await start($, on)
	const ui = await $.ui.mount({
		plugin: "peer",
		surface: "desktop",
		component: "Pane",
		requestId: "peer",
		props: PANE,
	})
	let drawn = JSON.stringify(await ui.drawn())
	expect(drawn).toContain("show all 20 lines")
	expect(drawn).not.toContain("line 20")
	await ui.press({ key: "more-d" })
	drawn = JSON.stringify(await ui.drawn())
	expect(drawn).toContain("line 20")
	expect(drawn).toContain("show less")
})

test("the footer counts messages the pane has not shown and toggles the pane", async ($, on) => {
	engineFooter(on)
	const ran = await start($, on)
	for (const surface of ["terminal", "desktop"] as const) {
		ran.length = 0
		const ui = await $.ui.mount({
			plugin: "peer",
			surface,
			component: "SessionMode",
			props: { modes: ["focus"] },
		})
		const drawn = JSON.stringify(await ui.drawn())
		expect(drawn).toContain("👀 Peer 4")
		expect(drawn).toContain("focus")
		await ui.press({ key: "peer-open" })
		expect(ran).toContainEqual(["ui.open", "peer"])
		await ui.press({ key: "peer-open" })
		expect(ran).toContainEqual(["ui.close", "peer"])
	}
})

test("the footer keeps another plugin's footer button beside the peer one", async ($, on) => {
	engineFooter(on, "☑️ Draft")
	await start($, on)
	const footer = await $.ui.mount({
		plugin: "peer",
		surface: "desktop",
		component: "SessionMode",
		props: { modes: ["focus"] },
	})
	const drawn = JSON.stringify(await footer.drawn())
	expect(drawn).toContain("☑️ Draft")
	expect(drawn).toContain("👀 Peer 4")
	// The labels are drawn once, by the inner part of the chain.
	expect(drawn.split("focus").length).toBe(2)
})

test("with two rooms, the footer has one button that counts both", async ($, on) => {
	engineFooter(on)
	const other = { ...ROOM, id: "auth-fix", started_at: "2026-10-05T19:00:00Z" }
	const note = {
		id: "z",
		at: "2026-10-05T19:01:00Z",
		from: "peer",
		to: "main",
		text: "hi",
	}
	await start(
		$,
		on,
		lines(
			{ event: "room", room: ROOM },
			{ event: "room", room: other },
			...MESSAGES.map((message) => ({
				event: "message",
				room: ROOM.id,
				message,
			})),
			{ event: "message", room: other.id, message: note },
			{ event: "ready" },
		),
	)
	const footer = await $.ui.mount({
		plugin: "peer",
		surface: "desktop",
		component: "SessionMode",
		props: { modes: [] },
	})
	const drawn = JSON.stringify(await footer.drawn())
	expect(drawn).toContain("👀 Peer 5")
	expect(drawn).not.toContain("peer-open-")
})

test("the pane starts a new room with the skill and the typed task", async ($, on) => {
	const ran: string[] = []
	on("command.run", (_, e) => {
		ran.push(`${e.command} ${e.args}`)
		return { text: "" } as never
	})
	await start($, on)
	const pane = await $.ui.mount({
		plugin: "peer",
		surface: "desktop",
		component: "Pane",
		requestId: "peer",
		props: PANE,
	})
	// With a room, the field waits behind its button.
	expect(await pane.find({ key: "new-room" })).toBeUndefined()
	await pane.press({ key: "new-room-open" })
	await pane.input({ key: "new-room", text: "   " })
	expect(ran).toEqual([])
	await pane.input({ key: "new-room", text: " add CSV export " })
	expect(ran).toEqual(["peer:peer add CSV export"])
	// The sent task replaces the field, so it is not sent twice.
	expect(await pane.find({ key: "new-room" })).toBeUndefined()
	expect(JSON.stringify(await pane.drawn())).toContain("Starting a room")
})

test("without a room, the pane shows the new room field at once", async ($, on) => {
	await start($, on, lines({ event: "ready" }))
	const pane = await $.ui.mount({
		plugin: "peer",
		surface: "desktop",
		component: "Pane",
		requestId: "peer",
		props: PANE,
	})
	expect(await pane.find({ key: "new-room" })).toBeDefined()
})

test("a member log with an entry over the desktop text limit still draws", async ($, on) => {
	await start(
		$,
		on,
		WATCH,
		lines({
			at: "2026-10-05T20:04:00Z",
			kind: "result",
			text: "x".repeat(20_000),
		}),
	)
	for (const surface of ["terminal", "desktop"] as const) {
		const ui = await $.ui.mount({
			plugin: "peer",
			surface,
			component: "Pane",
			requestId: "peer",
			props: PANE,
		})
		await ui.press({ key: "tab-reader" })
		const drawn = JSON.stringify(await ui.drawn())
		expect(drawn).toContain("xxxx")
		expect(drawn.length).toBeLessThan(15_000)
	}
})

test("end room acts on a second press within three seconds", async ($, on) => {
	const clock = mock.clock(on)
	const ran = await start($, on)
	const ui = await $.ui.mount({
		plugin: "peer",
		surface: "desktop",
		component: "Pane",
		requestId: "peer",
		props: PANE,
	})
	await ui.press({ key: "end" })
	expect(JSON.stringify(await ui.drawn())).toContain('"label":"confirm"')
	await clock.advance(3000)
	expect(JSON.stringify(await ui.drawn())).toContain("⏹ end")
	expect(ran.some((argv) => argv[1] === "end")).toBe(false)
	await ui.press({ key: "end" })
	await ui.press({ key: "end" })
	expect(ran.find((argv) => argv[1] === "end")).toEqual([
		"peer",
		"end",
		"csv-export",
		"--as",
		"user",
	])
})

test("an ended room shows every member stopped", async ($, on) => {
	// The room this session started stays in the pane once it has ended.
	on("prompt.submit", (_, e) => ({ text: e.text }) as never)
	await start(
		$,
		on,
		lines(
			{ event: "room", room: { ...ROOM, ended_at: "2026-10-05T21:00:00Z" } },
			{ event: "ready" },
		),
	)
	await $.tool.call({
		tool: "Bash",
		command: "peer start csv-export --agent claude",
	} as never)
	const ui = await $.ui.mount({
		plugin: "peer",
		surface: "desktop",
		component: "Pane",
		requestId: "peer",
		props: PANE,
	})
	const drawn = JSON.stringify(await ui.drawn())
	expect(drawn).toContain("○ reader")
	expect(drawn).not.toContain("● reader")
})

test("the plugin's own rows are hidden; others and the terminal's ctrl+o draw as the engine does", async ($, on) => {
	// Beneath the plugin, the engine's own drawing of a row.
	on(
		"ui.render",
		{ component: "UserMessage" },
		() => ({ type: "Text", props: {}, children: ["engine"] }) as never,
	)
	await start($, on)
	const row = (text: string, origin: unknown, isExpanded = false) =>
		$.ui.mount({
			plugin: "peer",
			surface: "desktop",
			component: "UserMessage",
			props: { text, origin, isExpanded } as never,
		})
	const delivery =
		"peer room csv-export: 2 new messages for main, delivered by the peer plugin; you do not run peer wait for this room.\n\n" +
		"[reader → main · 23:02:00]\nSee `main.go:42`"
	const plugin = { kind: "plugin", name: "peer" }
	// A submitted prompt, also inside the engine's framing, and an end note.
	for (const [text, origin] of [
		[delivery, plugin],
		[
			`The peer plugin sent a message:\n${delivery}\n\nThis is how Claude Code surfaces a prompt`,
			plugin,
		],
		[
			"peer room csv-export has ended. Its members no longer read or answer messages.",
			plugin,
		],
	] as const) {
		expect(
			JSON.stringify(await (await row(text, origin)).drawn()),
		).not.toContain("engine")
	}
	// The desktop has no ctrl+o and marks every row expanded: it still hides.
	expect(
		JSON.stringify(await (await row(delivery, plugin, true)).drawn()),
	).not.toContain("engine")
	const terminal = await $.ui.mount({
		plugin: "peer",
		surface: "terminal",
		component: "UserMessage",
		props: { text: delivery, origin: plugin, isExpanded: true } as never,
	})
	expect(JSON.stringify(await terminal.drawn())).toContain("engine")
	expect(
		JSON.stringify(
			await (
				await row("peer room is a nice idea", { kind: "composer" })
			).drawn(),
		),
	).toContain("engine")
})

test("another session's room of the checkout is not shown", async ($, on) => {
	engineFooter(on)
	const ran = await start($, on, WATCH, "", false)
	expect(ran.some((argv) => argv[0] === "ui.open")).toBe(false)
	const ui = await $.ui.mount({
		plugin: "peer",
		surface: "desktop",
		component: "Pane",
		requestId: "peer",
		props: PANE,
	})
	const drawn = JSON.stringify(await ui.drawn())
	expect(drawn).toContain("No active peer room")
	expect(drawn).not.toContain("reader joined")
	const footer = await $.ui.mount({
		plugin: "peer",
		surface: "desktop",
		component: "SessionMode",
		props: { modes: [] },
	})
	// The button shows without a room, with no count.
	expect(JSON.stringify(await footer.drawn())).toContain('"label":"👀 Peer"')
	// A failed join adopts nothing: alone, or inside a longer command line
	// that prints the room with main in it.
	await $.tool.call({
		tool: "Bash",
		command: "peer join csv-export main",
	} as never)
	await $.tool.call({
		tool: "Bash",
		command: "peer join csv-export main; peer status csv-export",
	} as never)
	expect(JSON.stringify(await footer.drawn())).toContain('"label":"👀 Peer"')
})

test("outside a Git checkout, a failing watch writes nothing to the chat", async ($, on) => {
	const clock = mock.clock(on)
	const logged: string[] = []
	on("ui.log", (_, e) => {
		logged.push(e.text)
		return { value: undefined } as never
	})
	const ran = await start($, on, {
		stderr: "peer: run this command inside the shared Git checkout\n",
	})
	for (let i = 0; i < 12; i++) {
		await clock.advance(10_000)
		await new Promise((resolve) => setTimeout(resolve, 5))
	}
	const tries = ran.filter((argv) => argv[1] === "watch").length
	expect(logged).toEqual([])
	// It tries again, less and less often: a cd into a checkout is picked up.
	expect(tries).toBeGreaterThan(1)
	expect(tries).toBeLessThan(10)
})

test("a state from before own counts the rooms it led as its own", async ($, on) => {
	// The session's state as an older module wrote it, until the first write.
	const old = {
		selected: null,
		tab: "chat",
		led: ["csv-export"],
		rooms: {},
		messages: {},
	}
	on("state.get", async (_, e, next) => {
		const read = (await next(e)) as {
			value: { value: unknown; version: number }
		}
		return (
			read.value.value === undefined
				? { value: { ...read.value, value: old } }
				: read
		) as never
	})
	const ran = await start($, on, WATCH, "", false)
	const ui = await $.ui.mount({
		plugin: "peer",
		surface: "desktop",
		component: "Pane",
		requestId: "peer",
		props: PANE,
	})
	expect(ran.some((argv) => argv[0] === "ui.open")).toBe(true)
	expect(JSON.stringify(await ui.drawn())).toContain("reader joined")
})

test("after a rewind the rooms its conversation started are its own and led again", async ($, on) => {
	// A rewind starts the session with an empty state; its chat holds the start.
	const use = {
		tool_use_id: "t1",
		tool: "Bash",
		input: { command: "peer start csv-export --agent claude" },
		result: { stdout: `${JSON.stringify(ROOM)}\n` },
	}
	on(
		"session.messages",
		() =>
			({ value: [{ role: "assistant", text: "", toolUses: [use] }] }) as never,
	)
	const ran = await start($, on, WATCH, "", false)
	expect(ran.some((argv) => argv[0] === "ui.open")).toBe(true)
	// Leading it reads main's unread count, so what main read is not delivered again.
	expect(ran.some((argv) => argv[1] === "status" && argv[2] === ROOM.id)).toBe(
		true,
	)
})

// mountBoards opens the boards tab of the pane on surface, with fs.read
// answering board.json with board.
async function mountBoards(
	$: Parameters<TestBody>[0],
	on: Parameters<TestBody>[1],
	surface: "terminal" | "desktop",
	boards: unknown[] = [BOARD],
) {
	const read: string[] = []
	on("fs.read", (_, e) => {
		read.push(e.path)
		const board = boards[Math.min(read.length, boards.length) - 1]
		return { value: JSON.stringify(board) } as never
	})
	await start($, on)
	const ui = await $.ui.mount({
		plugin: "peer",
		surface,
		component: "Pane",
		requestId: "peer",
		props: PANE,
	})
	await ui.press({ key: "tab-boards" })

	return { ui, drawn: JSON.stringify(await ui.drawn()), read }
}

test("the boards tab links the room's boards only", async ($, on) => {
	const { drawn, read } = await mountBoards($, on, "terminal")
	expect(drawn).toContain('"label":"boards"')
	// The title is the link, and the hint shows once under the boards.
	expect(drawn).toContain(
		'"href":"http://localhost:4747/b/a1","label":"Architecture ↗"',
	)
	expect(drawn).toContain("2 frames · 3 nodes · 1 open thread")
	expect(drawn.split("If a link does not open").length).toBe(2)
	expect(drawn).not.toContain("/b/b2")
	expect(read).toEqual(["/repo/docs/arch.board/board.json"])
	// The terminal lists the frames by order, with nodes and open threads.
	expect(drawn).toContain("Store · 3 nodes · 1 open thread")
	expect(drawn).toContain("Locks · 1 node · 0 open threads")
})

test("the board schematic draws every node, placed or not", async ($, on) => {
	const { drawn } = await mountBoards($, on, "desktop")
	// n3 has no position yet; the schematic still draws it.
	expect(drawn).toContain(">Cold start</tspan>")
	expect(drawn).not.toContain("Unplaced")
	expect(drawn).toContain('marker-end=\\"url(#arrow)\\"')
})

type Rect = { x: number; y: number; w: number; h: number }

// rects gives the boxes of an Svg in drawing order: frames, then cards.
const rects = (svg: string): Rect[] =>
	[
		...svg.matchAll(
			/<rect x="([\d.-]+)" y="([\d.-]+)" width="([\d.]+)" height="([\d.]+)" rx/g,
		),
	].map((m) => ({
		x: Number(m[1]),
		y: Number(m[2]),
		w: Number(m[3]),
		h: Number(m[4]),
	}))
const within = (inner: Rect, outer: Rect) =>
	inner.x >= outer.x &&
	inner.y >= outer.y &&
	inner.x + inner.w <= outer.x + outer.w &&
	inner.y + inner.h <= outer.y + outer.h

test("the schematic ignores stored geometry and nests nodes in their frames", () => {
	const svg = boardSvg(BOARD as PeerBoard) ?? ""
	const unplaced = {
		...BOARD,
		nodes: BOARD.nodes.map(({ x, y, w, h, ...node }) => node),
	}
	// A board nobody opened in the browser looks the same.
	expect(boardSvg(unplaced as PeerBoard)).toBe(svg)
	const boxes = rects(svg)
	expect(boxes.length).toBe(5)
	const [store, locks, n1, n2, n3] = boxes as [Rect, Rect, Rect, Rect, Rect]
	for (const box of [locks, n1, n3]) expect(within(box, store)).toBe(true)
	expect(within(n2, locks)).toBe(true)
	// Nothing in a frame overlaps.
	const overlap = (a: Rect, b: Rect) =>
		a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h
	expect(overlap(locks, n1) || overlap(locks, n3) || overlap(n1, n3)).toBe(
		false,
	)
})

test("every edge is drawn, and an edge between frames orders the frames", () => {
	const svg =
		boardSvg({
			title: "Edges",
			nodes: [
				{ id: "f5", type: "frame", title: "Later", order: 1 },
				{ id: "f1", type: "frame", title: "First", order: 2 },
				{ id: "n3", type: "card", text: "a", parentId: "f1" },
				{ id: "n6", type: "card", text: "b", parentId: "f5" },
				{ id: "n7", type: "card", text: "c", parentId: "f5" },
			],
			edges: [
				{ id: "e1", from: "n3", to: "n6" },
				{ id: "e2", from: "n6", to: "n7" },
				{ id: "e3", from: "n7", to: "n6" },
				{ id: "e4", from: "n7", to: "n7" },
				{ id: "e5", from: "f1", to: "n3" },
			],
			threads: [],
		}) ?? ""
	// A cycle ends; both its edges, the loop and the frame's edge to its
	// own child are drawn, each with its arrow.
	expect(svg.match(/marker-end="url\(#arrow\)"/g)?.length).toBe(5)
	// n3 -> n6 puts f1 above f5, against their order.
	const [later, first] = rects(svg) as [Rect, Rect]
	expect(first.y + first.h).toBeLessThan(later.y)
	// The edge joins the cards themselves, not their frames.
	const [n3, n6] = rects(svg).slice(2) as [Rect, Rect]
	const line = svg.match(
		/<line x1="([\d.-]+)" y1="([\d.-]+)" x2="([\d.-]+)" y2="([\d.-]+)"/,
	)
	expect(Number(line?.[2])).toBe(n3.y + n3.h)
	expect(Number(line?.[4])).toBe(n6.y)
})

test("edges keep clear of the boxes they do not join", () => {
	const board = {
		title: "Clear",
		nodes: [
			{ id: "f1", type: "frame", title: "Plan", order: 1 },
			{ id: "n1", type: "card", text: "check", parentId: "f1" },
			{ id: "n2", type: "card", text: "write", parentId: "f1" },
			{ id: "f2", type: "frame", title: "Risks", order: 2 },
			{ id: "n3", type: "card", text: "retry", parentId: "f2" },
		],
		edges: [
			{ id: "e1", from: "n1", to: "n2", label: "then" },
			{ id: "e2", from: "n2", to: "n1", label: "fix" },
			{ id: "e3", from: "n1", to: "n3" },
		],
		threads: [],
	} as PeerBoard
	const svg = boardSvg(board) ?? ""
	const [, , n1, n2] = rects(svg) as [Rect, Rect, Rect, Rect, Rect]
	// The labeled edges leave a gap that holds their labels.
	expect(n2.y - (n1.y + n1.h)).toBeGreaterThanOrEqual(30)
	// An edge and its reverse do not share a line.
	const lines = [
		...svg.matchAll(/<line x1="([\d.-]+)" y1="[\d.-]+" x2="([\d.-]+)"/g),
	].map((m) => m[1])
	expect(new Set(lines).size).toBe(lines.length)
	// n1 -> n3 would cross n2, so it goes around on the right of the frame.
	const around = svg.match(/<polyline points="([^"]+)"/)?.[1] ?? ""
	const xs = around.split(" ").map((p) => Number(p.split(",")[0]))
	expect(Math.max(...xs)).toBeGreaterThan(n2.x + n2.w + 12)
})

// labels gives each edge label's horizontal extent, about 6 units a glyph.
const labels = (svg: string) =>
	[
		...svg.matchAll(
			/<text x="([\d.-]+)" y="([\d.-]+)" text-anchor="(\w+)" font-size="10" fill="#888">([^<]+)</g,
		),
	].map((m) => {
		const x = Number(m[1])
		const w = Array.from(m[4] ?? "").length * 6
		const left =
			{ start: x, end: x - w, middle: x - w / 2 }[m[3] ?? "middle"] ?? x
		return { left, right: left + w, y: Number(m[2]), text: m[4] ?? "" }
	})
test("the edges of one pair share a label and never one path", () => {
	const nodes = [
		{ id: "f1", type: "frame", title: "W".repeat(30), order: 1 },
		{ id: "n1", type: "card", text: "a", parentId: "f1" },
		{ id: "n2", type: "card", text: "b", parentId: "f1" },
		{ id: "n3", type: "card", text: "c", parentId: "f1" },
	]
	const cases = [
		// An edge and its reverse.
		[
			["n1", "n2", "forward"],
			["n2", "n1", "backward"],
		],
		// Three edges one way.
		[
			["n1", "n2", "first"],
			["n1", "n2", "second"],
			["n1", "n2", "third"],
		],
		// A pair that goes around n2.
		[
			["n1", "n2", ""],
			["n2", "n3", ""],
			["n1", "n3", "forward"],
			["n3", "n1", "backward"],
		],
		// A frame and its own card.
		[
			["f1", "n1", "forward"],
			["n1", "f1", "backward"],
		],
	]
	for (const edges of cases) {
		const svg =
			boardSvg({
				title: "Pairs",
				nodes,
				edges: edges.map(([from, to, label], i) => ({
					id: `e${i}`,
					from,
					to,
					...(label ? { label } : {}),
				})),
				threads: [],
			} as PeerBoard) ?? ""
		const named = edges.map(([, , label]) => label ?? "").filter(Boolean)
		const tags = labels(svg).filter((l) =>
			named.some((n) => l.text.includes(n)),
		)
		// At most one label shows for the pair, cut to the room it has;
		// every edge keeps its full label in its <title>.
		expect(tags.length).toBeLessThanOrEqual(1)
		for (const name of named) expect(svg).toContain(`<title>${name}</title>`)
		const paths = [
			...svg.matchAll(
				/<line x1="([^"]+)" y1="([^"]+)" x2="([^"]+)" y2="([^"]+)"|<polyline points="([^"]+)"/g,
			),
		].map((m) => m.slice(1).join(" "))
		expect(new Set(paths).size).toBe(paths.length)
		// A shown label sits under the frame's title bar.
		expect(tags.every((tag) => tag.y > 22)).toBe(true)
	}
})

test("a frame's thread badge has its own slot after a wide title", () => {
	const svg =
		boardSvg({
			title: "Wide",
			nodes: [{ id: "f1", type: "frame", title: "W".repeat(30), order: 1 }],
			edges: [],
			threads: [{ target: { kind: "frame", id: "f1" }, resolved: false }],
		}) ?? ""
	const clip = svg.match(
		/<clipPath id="f0"><rect x="([\d.-]+)" y="[\d.-]+" width="([\d.]+)"/,
	)
	const badge = labels(svg).find((l) => l.text.startsWith("💬"))
	expect(Number(clip?.[1]) + Number(clip?.[2])).toBeLessThanOrEqual(
		(badge?.left ?? 0) - 4,
	)
})

test("a box tells its full text and its open threads", () => {
	const text = "a long card text ".repeat(10)
	const board = {
		title: "Notes",
		nodes: [
			{ id: "f1", type: "frame", title: "Plan", order: 1 },
			{ id: "n1", type: "card", text, parentId: "f1" },
		],
		edges: [],
		threads: [
			{ target: { kind: "node", id: "n1" }, resolved: false },
			{ target: { kind: "node", id: "n1" }, resolved: false },
			{ target: { kind: "frame", id: "f1" }, resolved: true },
		],
	} as PeerBoard
	const svg = boardSvg(board) ?? ""
	expect(svg).toContain(`<title>${text}</title>`)
	expect(svg).toContain(">💬 2</text>")
	// A resolved thread has no badge.
	expect(svg.match(/💬/g)?.length).toBe(1)
	expect(boardStats(board)).toBe("1 frame · 1 node · 2 open threads")
})

test("a long card text is wrapped and cut inside its card", () => {
	const text = "word ".repeat(80) + "x".repeat(60)
	const svg =
		boardSvg({
			title: "Long",
			nodes: [{ id: "n1", type: "card", text }],
			edges: [],
			threads: [],
		}) ?? ""
	const spans = [...svg.matchAll(/<tspan[^>]*>([^<]*)<\/tspan>/g)].map(
		(m) => m[1] ?? "",
	)
	expect(spans.length).toBe(4)
	expect(spans.every((line) => line.length <= 21)).toBe(true)
	expect(spans[3]?.endsWith("…")).toBe(true)
	expect(svg).toContain('clip-path="url(#c0)"')
})

test("a loop on the top card keeps its label inside the drawing", () => {
	const svg =
		boardSvg({
			title: "Loop",
			nodes: [{ id: "n1", type: "card", text: "a" }],
			edges: [{ id: "e1", from: "n1", to: "n1", label: "repeat" }],
			threads: [],
		}) ?? ""
	const [left, top, width] = (svg.match(/viewBox="([^"]+)"/)?.[1] ?? "")
		.split(" ")
		.map(Number) as [number, number, number]
	const tag = labels(svg).find((l) => l.text === "repeat")
	// The label's glyphs rise about 8 units over its baseline.
	expect(tag && tag.left >= left && tag.right <= left + width).toBe(true)
	expect((tag?.y ?? 0) - 8).toBeGreaterThanOrEqual(top)
})

test("an emoji text is cut between characters, not inside one", () => {
	const svg =
		boardSvg({
			title: "Emoji",
			nodes: [
				{ id: "f1", type: "frame", title: "😀".repeat(40), order: 1 },
				{ id: "n1", type: "card", text: "😀".repeat(200), parentId: "f1" },
			],
			edges: [],
			threads: [],
		}) ?? ""
	const lone =
		/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/
	expect(lone.test(svg)).toBe(false)
	expect(svg).toContain("😀😀…</text>")
	expect(svg).toContain("😀😀…</tspan>")
})

test("a deeply nested card is still drawn", () => {
	const frames = Array.from({ length: 40 }, (_, i) => ({
		id: `f${i}`,
		type: "frame" as const,
		title: `level ${i}`,
		order: i,
		parentId: i > 0 ? `f${i - 1}` : undefined,
	}))
	const svg =
		boardSvg({
			title: "Deep",
			nodes: [
				...frames,
				{ id: "n1", type: "card", text: "leaf", parentId: "f39" },
			],
			edges: [],
			threads: [],
		}) ?? ""
	expect(svg).toContain(">leaf</tspan>")
})

test("a board over the Svg limit draws its frames only", async ($, on) => {
	const cards = Array.from({ length: 2000 }, (_, i) => ({
		id: `n${i}`,
		type: "card",
		text: `card ${i} ${"x".repeat(30)}`,
		parentId: "f1",
		x: (i % 40) * 250,
		y: Math.floor(i / 40) * 100,
	}))
	const big = { ...BOARD, nodes: [BOARD.nodes[0], ...cards], edges: [] }
	const { drawn } = await mountBoards($, on, "desktop", [big])
	expect(drawn).toContain('"source":"<svg')
	expect(drawn).toContain(">Store</text>")
	expect(drawn).not.toContain("card 1 ")
})

test("a board written twice in one second is read again", async ($, on) => {
	const [store, ...rest] = BOARD.nodes
	const renamed = {
		...BOARD,
		nodes: [{ ...store, title: "Store v2" }, ...rest],
	}
	const { ui } = await mountBoards($, on, "terminal", [BOARD, renamed])
	// Same mtime, as board.json has whole seconds; the revision grew.
	listing.boards = BOARD_LIST.replace('"revision":1', '"revision":2')
	try {
		await ui.press({ key: "tab-boards" })
		expect(JSON.stringify(await ui.drawn())).toContain("Store v2 · 3 nodes")
	} finally {
		listing.boards = BOARD_LIST
	}
})

const toolUse = (command: string) =>
	({
		tool_use_id: "t1",
		tool: "Bash",
		input: { command },
		isRunning: false,
		isErrored: false,
		isInterrupted: false,
	}) as never

test("a peer send row is the message card; a line the shell rewrites stays the command", async ($, on) => {
	on("ui.render", { component: "ToolUse" }, ($, e) =>
		$.ui.resolve(e).Text({ children: "engine row" }),
	)
	await start($, on)
	const card = await $.ui.mount({
		plugin: "peer",
		surface: "desktop",
		component: "ToolUse",
		props: toolUse(
			'peer send csv-export --as reader --to main --text "See main.go:42"',
		),
	})
	const drawn = JSON.stringify(await card.drawn())
	expect(drawn).toContain("👀 csv-export")
	expect(drawn).toContain("reader")
	expect(drawn).toContain("[main.go:42](file:///repo/main.go)")
	// What $(…) prints is what the room got, so the card would show other text.
	for (const command of [
		'peer send csv-export --as main --text "done: $(cat notes)"',
		"peer send csv-export --as main <<EOF",
		"peer send csv-export --as main --text hi && peer end csv-export --as main",
	]) {
		const row = await $.ui.mount({
			plugin: "peer",
			surface: "desktop",
			component: "ToolUse",
			props: toolUse(command),
		})
		expect(JSON.stringify(await row.drawn())).toContain("engine row")
	}
})

test("/room toggles the pane and @ offers the room's members", async ($, on) => {
	on("prompt.autocomplete", () => ({ suggestions: [] }) as never)
	const ran = await start($, on)
	await $.command.run({ command: "room", args: "" } as never)
	expect(ran).toContainEqual(["ui.open", "peer"])
	await $.command.run({ command: "room", args: "" } as never)
	expect(ran).toContainEqual(["ui.close", "peer"])
	// The kit raises the event, though Engine does not list it.
	const typeahead = $.prompt as unknown as {
		autocomplete: (e: unknown) => Promise<{ suggestions: { text: string }[] }>
	}
	const { suggestions } = await typeahead.autocomplete({
		text: "ask @re",
		cursor: 7,
		token: "@re",
		start: 4,
	})
	expect(suggestions.map((one) => one.text)).toContain("@reader")
})

test("the dialog of a peer end says which members still run", async ($, on) => {
	const notices: string[] = []
	on("ui.notice", (_, e) => {
		notices.push(String((e as { text?: string }).text))
		return { value: undefined } as never
	})
	await start($, on)
	await $.tool.call({
		tool: "Bash",
		command: "peer end csv-export --as main",
		tool_use_id: "t2",
	} as never)
	expect(notices).toContainEqual("peer: ends csv-export; still running: reader")
})

test("a member's edits replay one file at a time, from whole log entries only", async ($, on) => {
	const log = lines(
		{
			kind: "tool",
			text: 'Edit {"file_path":"/repo/main.go","old_string":"a"}',
		},
		// The log cut this input, so its path may be cut too.
		{ kind: "tool", text: 'Write {"file_path":"/repo/very/long…' },
		{ kind: "tool", text: "edit store.go web/app.ts" },
	)
	const ran = await start($, on, WATCH, log)
	const ui = await $.ui.mount({
		plugin: "peer",
		surface: "desktop",
		component: "Pane",
		requestId: "peer",
		props: PANE,
	})
	await ui.press({ key: "tab-reader" })
	let drawn = JSON.stringify(await ui.drawn())
	expect(drawn).toContain("replay 3 edits")
	await ui.press({ key: "replay" })
	drawn = JSON.stringify(await ui.drawn())
	expect(drawn).toContain("main.go")
	expect(drawn).toContain("step 1 of 3")
	expect(ran).toContainEqual([
		"git",
		"-C",
		"/repo",
		"diff",
		"--",
		"/repo/main.go",
	])
	await ui.press({ key: "replay-next" })
	expect(JSON.stringify(await ui.drawn())).toContain("store.go")
})

test("the terminal draws a board as cells of its schematic", async ($, on) => {
	on("fs.read", () => ({ value: JSON.stringify(BOARD) }) as never)
	await start($, on)
	const ui = await $.ui.mount({
		plugin: "peer",
		surface: "terminal",
		component: "Pane",
		requestId: "peer",
		props: PANE,
	})
	await ui.press({ key: "tab-boards" })
	await new Promise((resolve) => setTimeout(resolve, 50))
	const drawn = JSON.stringify(await ui.drawn())
	const raster = /"columns":(\d+),"rows":(\d+),"cells":"([^"]+)"/.exec(drawn)
	expect(raster).not.toBeNull()
	const [, columns, rows, cells] = raster ?? []
	// Three little-endian u32 per cell, in padded base64.
	expect(Math.ceil((Number(columns) * Number(rows) * 12) / 3) * 4).toBe(
		cells?.length ?? 0,
	)
	expect(drawn).toContain("Store · 3 nodes")
})
