import type { EngineInterface as Engine, Register } from "claude-code"

import type {
	PeerBoard,
	PeerBoardRef,
	PeerLogEntry,
	PeerMark,
	PeerMember,
	PeerMessage,
	PeerRoom,
	PeerState,
} from "../types"
import {
	boardRaster,
	boardStats,
	boardSvg,
	frameLines,
} from "./board-preview.ts"

const PANE = "peer"
const EVERYONE = "*"
// BOARDS is the tab value of the room's boards; ":" keeps it apart from roles.
const BOARDS = ":boards"
// ICON stands for peer in the prompt footer.
const ICON = "👀"
// SKILL is the plugin's slash command that starts a room.
const SKILL = "peer:peer"

// All of the plugin's session state is one value: the engine lists the
// values a module reads and writes, so each reference is a literal.
const STATE = { plugin: "peer", key: "state" } as const

const INITIAL: PeerState = {
	rooms: {},
	messages: {},
	selected: null,
	tab: "chat",
	confirm: null,
	led: [],
	own: [],
	watchError: null,
	ackErrors: {},
	log: [],
	// delivered marks, per led room, the last message for main that is in
	// the chat, so a reload goes on from it.
	delivered: {},
	// notified lists the led rooms whose end main has been told about.
	notified: [],
	expanded: [],
	seen: {},
}

const rooms = "rooms"
const messages = "messages"
const selected = "selected"
const tab = "tab"
const confirm = "confirm"
const led = "led"
const own = "own"
const watchError = "watchError"
const ackErrors = "ackErrors"
const log = "log"
const delivered = "delivered"
const notified = "notified"
const expanded = "expanded"
const seen = "seen"

const HANDOFF =
	"The peer plugin reads this room for main and puts its messages into this chat as a turn of their own, once this session is free. Do not run peer wait for this room; go on with the task."

// Module variables start over on a reload; everything drawn is in $.state.
let isReady = false
// watch's last failure, so the transcript tells of each kind once, and its
// pause before the next try.
let lastWatchError = ""
let watchRetry = 2500
let isStarted = false
let isPumping = false
let isPumpWanted = false
// The rooms restore found this session started, led once watch is ready.
let toLead: string[] = []
// Whether the person holds the pane's keyboard: then a new message does not
// scroll it, so what they read stays put.
let isPaneFocused = false
// The host's offset from UTC in minutes: the module's environment does not
// know the host's time zone, so times are shifted by hand.
let utcOffset = 0
// The checkout's files, so a message's file:line becomes a link.
let files = { repo: "", paths: new Set<string>() }
// The boards of each room, with their board.json once read, while the
// boards tab shows; error says why the list could not be read.
type BoardView = { ref: PeerBoardRef; board?: PeerBoard; error?: string }
let boardViews: Record<string, { boards: BoardView[]; error?: string }> = {}
// The pane's new room field: whether a room's pane shows it, and the task
// sent from it until its turn ends or a room starts.
let isComposing = false
let startingTask = ""
let isStartingTurn = false
// The rooms by id as watch last printed them, for drawings that must not
// read the state: a transcript row that read it would be drawn again on
// every write.
let roomIndex: Record<string, PeerRoom> = {}
// What the running turn reads when it is a delivery: the spinner's line,
// and the author the prompt box suggests answering once it ends.
let deliveryNote = ""
let deliveryAuthor = ""
// What each queued delivery's turn will read, by its text, made from the
// messages themselves: a message's text never names an author here.
const deliveries = new Map<string, { line: string; author: string }>()
// When each room last raised a notification for a message to main.
const notifiedAt: Record<string, number> = {}
// The member whose edits the pane replays, the step it shows, and that
// step's file diff once git printed it.
let replay: { role: string; index: number } | null = null
let replayDiff: {
	key: string
	text: string
	error?: string
	isLoaded?: boolean
} | null = null

// starting sets the task the pane shows as starting and draws the pane.
function starting($: Engine, task: string) {
	startingTask = task
	isStartingTurn = false
	$.ui.invalidate("ui.render")
}

// normalize fills in the fields a state lacks; a state without own takes
// the rooms it led as its own.
function normalize(value: Partial<PeerState> | undefined): PeerState {
	return { ...INITIAL, ...value, own: value?.own ?? value?.led ?? [] }
}

// get reads a field of the plugin's state; while a drawing reads it, a
// later put draws that drawing again.
async function get<K extends keyof PeerState>(
	$: Engine,
	field: K,
): Promise<PeerState[K]> {
	const { value } = await $.state.get(STATE)
	return normalize(value)[field]
}

// snapshot reads the whole state at once: a drawing reads it once, not
// once per field.
async function snapshot($: Engine): Promise<PeerState> {
	const { value } = await $.state.get(STATE)
	return normalize(value)
}

// put applies fn to a field and writes the state, again when another write
// came first. A write that changes nothing is skipped, since each write
// draws every drawing that reads the state again.
async function put<K extends keyof PeerState>(
	$: Engine,
	field: K,
	fn: (old: PeerState[K]) => PeerState[K],
) {
	for (;;) {
		const { value, version } = await $.state.get(STATE)
		const old = normalize(value)
		const next = fn(old[field])
		if (next === old[field]) return
		const written = await $.state.set(
			STATE,
			{ ...old, [field]: next },
			{ ifVersion: version },
		)
		if (written.isSet) return
	}
}

// begin starts the session's background work once; session.start calls it,
// and so do the first hooks of a mod loaded into a running session.
async function begin($: Engine) {
	if (isStarted) return
	isStarted = true
	// A pane left open by the module before a reload is drawn by this one.
	$.ui.invalidate("ui.render")
	await restore($)
	await $.command
		.register({
			name: "room",
			description: "peer: open or close the pane of this session's room",
			immediate: true,
		})
		.catch(() => ({}))
	watch($)
	$.clock.every(3000, () => void refresh($))
	void loadOffset($)
	$.clock.every(60_000, () => void loadFiles($))
	void loadFiles($)
}

// restore finds this session's rooms in its conversation when its state
// starts empty while the chat goes on, as after a rewind or a resume: a
// room joined there is its own again, and one started there is led again
// once watch is ready.
async function restore($: Engine) {
	if ((await $.state.get(STATE)).value !== undefined) return
	const found = await $.session.messages().catch(() => [])
	if ("deny" in found) return
	const joined: string[] = []
	for (const use of found.flatMap((m) => m.toolUses)) {
		if (
			use.tool !== "Bash" ||
			use.isError ||
			typeof use.input.command !== "string"
		)
			continue
		const all = shellCommands(use.input.command)
		const commands = all
			.map(peerArgs)
			.filter((args): args is string[] => args !== null)
		const stdout = (use.result as { stdout?: string } | undefined)?.stdout ?? ""
		const [cmd, id, role] = commands[0] ?? []
		if (
			all.length === 1 &&
			cmd === "join" &&
			id &&
			role &&
			joinedRoom(stdout, id, role)
		)
			joined.push(id)
		const room = commands.some((args) => args[0] === "start")
			? await startedRoom(stdout)
			: null
		if (room) toLead.push(room.id)
	}
	const mine = [...new Set([...joined, ...toLead])]
	if (mine.length > 0) await put($, own, () => mine)
}

// loadOffset reads the host's UTC offset, as +0300, and draws the times again.
async function loadOffset($: Engine) {
	try {
		const { stdout } = await $.process.run(["date", "+%z"], { timeoutMs: 5000 })
		const match = /^([+-])(\d\d)(\d\d)$/.exec(stdout.trim())
		if (!match) return
		utcOffset =
			(match[1] === "-" ? -1 : 1) * (Number(match[2]) * 60 + Number(match[3]))
		$.ui.invalidate("ui.render")
	} catch {
		// times stay in UTC
	}
}

async function loadFiles($: Engine) {
	try {
		const repo = current(await snapshot($))?.repo ?? (await $.session.cwd())
		const { exitCode, stdout } = await $.process.run(
			["git", "-C", repo, "ls-files"],
			{ timeoutMs: 10_000 },
		)
		if (exitCode === 0)
			files = { repo, paths: new Set(stdout.split("\n").filter(Boolean)) }
	} catch {
		// no links until the next try
	}
}

// toNewest scrolls the pane to its top, where the newest message is.
function toNewest($: Engine) {
	void $.ui.scroll({ in: PANE, to: "start" }).catch(() => ({}))
}

function peer($: Engine, argv: string[], timeoutMs = 30_000) {
	return $.process.run(["peer", ...argv], { timeoutMs })
}

// activeRooms lists this session's active rooms, the ones it leads first,
// then the newest first. Other sessions' rooms of the checkout are theirs:
// the plugin does not show them.
function activeRooms(st: PeerState) {
	const isLed = (room: PeerRoom) => (st.led.includes(room.id) ? 0 : 1)
	return Object.values(st.rooms)
		.filter((room) => !room.ended_at && st.own.includes(room.id))
		.sort(
			(a, b) => isLed(a) - isLed(b) || b.started_at.localeCompare(a.started_at),
		)
}

// current is the room the pane shows: the one picked, else the first active.
function current(st: PeerState) {
	const picked =
		st.selected && st.own.includes(st.selected)
			? st.rooms[st.selected]
			: undefined
	return picked ?? activeRooms(st)[0] ?? null
}

async function openPane($: Engine) {
	const opened = await $.ui
		.open({ id: PANE, title: "peer" })
		.catch(() => ({ isPlaced: false }))
	if (opened.isPlaced) toNewest($)
	return opened
}

// togglePane closes the pane where it shows, and opens it otherwise.
async function togglePane($: Engine) {
	const isShown = (await $.ui.panes()).some(
		(pane) => pane.id === PANE && pane.isShown,
	)
	if (isShown) {
		await $.ui.close({ id: PANE })
		return "closed"
	}
	const { isPlaced } = await openPane($)
	return isPlaced ? "opened" : "not placed"
}

// unseen counts the messages in the session's rooms that came since the
// pane last showed them.
function unseen(st: PeerState) {
	return activeRooms(st).reduce(
		(sum, room) =>
			sum + (st.messages[room.id] ?? []).length - (st.seen[room.id] ?? 0),
		0,
	)
}

// watch keeps rooms and messages current from peer watch, which reads
// the store without moving anyone's cursor.
function watch($: Engine) {
	void (async () => {
		let buffer = ""
		let stderr = ""
		try {
			for await (const { stream, text } of $.process.spawn({
				argv: ["peer", "watch"],
			})) {
				if (stream === "stderr") {
					stderr += text
					continue
				}
				buffer += text
				const lines = buffer.split("\n")
				buffer = lines.pop() ?? ""
				await apply($, lines)
			}
		} catch (err) {
			stderr += String(err)
		}
		const reason = stderr.trim().slice(0, 300) || "no output"
		// A session outside a Git checkout has no rooms: that is not an error,
		// and a cd into a checkout is picked up by the next try.
		const isOutside = /inside the shared Git checkout/.test(reason)
		if (!isOutside && reason !== lastWatchError)
			$.ui.log(`peer watch stopped: ${reason}`, { to: "transcript" })
		lastWatchError = reason
		const isOldCli = /usage|unknown|not found|no such file/i.test(stderr)
		const failure = isOldCli
			? "peer watch failed: update the peer CLI (peer update or brew upgrade --cask peer)"
			: `peer watch stopped: ${reason.slice(0, 200)}; retrying`
		await put($, watchError, () => (isOutside ? null : failure))
		watchRetry = Math.min(watchRetry * 2, 60_000)
		$.clock.after(watchRetry, () => watch($))
	})()
}

async function apply($: Engine, lines: string[]) {
	const roomEvents: PeerRoom[] = []
	const newMessages: Record<string, PeerMessage[]> = {}
	let ready = false
	for (const line of lines) {
		if (!line.trim()) continue
		let event: { event?: string; room?: unknown; message?: PeerMessage }
		try {
			event = JSON.parse(line)
		} catch {
			continue
		}
		if (
			event.event === "room" &&
			event.room &&
			typeof event.room === "object"
		) {
			roomEvents.push(event.room as PeerRoom)
		} else if (
			event.event === "message" &&
			typeof event.room === "string" &&
			event.message
		) {
			const list = newMessages[event.room] ?? []
			list.push(event.message)
			newMessages[event.room] = list
		} else if (event.event === "ready") {
			ready = true
		}
	}
	const before = await get($, rooms)
	const known = await get($, messages)
	// A restarted watch replays the transcript: keep each message once.
	const fresh: Record<string, PeerMessage[]> = {}
	for (const [id, list] of Object.entries(newMessages)) {
		const ids = new Set((known[id] ?? []).map((m) => m.id))
		const added = list.filter((m) => !ids.has(m.id))
		if (added.length > 0) fresh[id] = added
	}
	if (roomEvents.length > 0) {
		await put($, rooms, (all) => {
			const next = { ...all }
			for (const room of roomEvents) next[room.id] = room
			return next
		})
	}
	if (Object.keys(fresh).length > 0) {
		await put($, messages, (all) => {
			const next = { ...all }
			for (const [id, list] of Object.entries(fresh))
				next[id] = [...(next[id] ?? []), ...list]
			return next
		})
	}
	await put($, watchError, (old) => (old === null ? old : null))
	if (ready) {
		watchRetry = 2500
		lastWatchError = ""
	}
	if (isReady) {
		const mine = new Set(await get($, own))
		// Notifications are main's: a session that is a member of the room
		// too would raise each one twice.
		const leads = new Set(await get($, led))
		for (const room of roomEvents) {
			const was = before[room.id]
			if (!mine.has(room.id)) continue
			if (room.ended_at && was && !was.ended_at) {
				$.ui.toast(`peer: room ${room.id} ended`)
				if (leads.has(room.id)) notify($, `room ${room.id} ended`)
			}
		}
		for (const [id, list] of Object.entries(fresh)) {
			if (!mine.has(id)) continue
			for (const m of list) {
				if (m.from === "peer") $.ui.toast(`${id}: ${m.text}`)
			}
			// A member that writes to main alone may wait for an answer. The
			// notification names the author and the room, never the text: the
			// person's notification hooks may send it off the machine.
			const direct = list.find(
				(m) => m.to === "main" && !["main", "peer", "user"].includes(m.from),
			)
			const now = Date.now()
			if (direct && leads.has(id) && now - (notifiedAt[id] ?? 0) > 60_000) {
				notifiedAt[id] = now
				notify($, `${direct.from} wrote to main in ${id}`)
			}
		}
		const st = await snapshot($)
		if (!isPaneFocused && st.tab === "chat" && fresh[current(st)?.id ?? ""])
			toNewest($)
	}
	if (ready) {
		// A watch started after a room ended does not list it: read the rest
		// of a led room's transcript, then its state, from log and status.
		const listed = new Set(roomEvents.map((room) => room.id))
		const isLed = new Set(await get($, led))
		const mine = new Set(await get($, own))
		for (const room of Object.values(await get($, rooms))) {
			if (room.ended_at || listed.has(room.id) || !mine.has(room.id)) continue
			if (isLed.has(room.id) && !(await readLog($, room.id))) continue
			const { exitCode, stdout } = await peer($, ["status", room.id])
			if (exitCode === 0 && stdout.trim()) {
				const status = JSON.parse(stdout) as PeerRoom
				await put($, rooms, (all) => ({ ...all, [status.id]: status }))
			}
		}
		if (!isReady) {
			// Lead the restored rooms now that their messages are in: lead
			// marks what main read against them.
			const all = await get($, rooms)
			for (const id of toLead)
				if (all[id] && !all[id].ended_at) await lead($, id)
			toLead = []
			isReady = true
			if (activeRooms(await snapshot($)).length > 0) void openPane($)
		}
	}
	roomIndex = await get($, rooms)
	void pump($)
}

// notify raises a native notification; a person who turned them off, or a
// surface without them, is no error.
function notify($: Engine, text: string) {
	void $.ui.notify(text, { title: "peer" }).catch(() => ({}))
}

// readLog adds a room's whole transcript from peer log to the messages,
// for what a watch may not have printed yet; it says whether it could.
async function readLog($: Engine, id: string) {
	const { exitCode, stdout, isStdoutTruncated } = await peer($, [
		"log",
		id,
		"--json",
	])
	if (exitCode !== 0) return false
	if (isStdoutTruncated) {
		await put(
			$,
			watchError,
			() =>
				`the transcript of ${id} is over 4 MiB; read its end with peer log ${id}`,
		)
		return false
	}
	const list = stdout
		.split("\n")
		.filter((line) => line.trim())
		.map((line) => JSON.parse(line) as PeerMessage)
	await put($, messages, (all) => {
		const ids = new Set((all[id] ?? []).map((m) => m.id))
		return {
			...all,
			[id]: [...(all[id] ?? []), ...list.filter((m) => !ids.has(m.id))],
		}
	})
	return true
}

// lead makes this session main's reader for room id. Messages come from
// peer watch's transcript, not from wait, so a reload or a failed delivery
// loses none; main's cursor moves only after a delivery. A reload between
// a queued turn and its mark brings that batch again: at least once, not
// exactly once.
async function lead($: Engine, id: string, isNew = false) {
	await put($, own, (list) => (list.includes(id) ? list : [...list, id]))
	if ((await get($, led)).includes(id)) return
	if (!(id in (await get($, delivered)))) {
		// From a new room, deliver everything; otherwise start after what main
		// already read with wait, which status counts as not unread.
		let mark: PeerMark | null = null
		if (!isNew) {
			const { stdout } = await peer($, ["status", id])
			const unread =
				(JSON.parse(stdout || "{}") as PeerRoom).members?.find(
					(m) => m.role === "main",
				)?.unread ?? 0
			const list = forMain((await get($, messages))[id] ?? [])
			const last = list[list.length - unread - 1]
			mark = last ? { id: last.id, at: last.at } : null
		}
		await put($, delivered, (all) => ({ ...all, [id]: mark }))
	}
	await put($, led, (list) => [...list, id])
	void pump($)
}

function forMain(list: PeerMessage[]) {
	return list.filter(
		(m) => m.from !== "main" && (m.to === "main" || m.to === EVERYONE),
	)
}

// after returns the messages of list that come after mark.
function after(list: PeerMessage[], mark: PeerMark | null) {
	if (!mark) return list
	const i = list.findIndex((m) => m.id === mark.id)
	if (i >= 0) return list.slice(i + 1)
	const at = Date.parse(mark.at)
	return list.filter((m) => Date.parse(m.at) > at)
}

// pump puts what is new for main in each led room into the chat, in order;
// a mark moves only once its batch is there, and a failure is tried again.
async function pump($: Engine) {
	if (!isReady) return
	// A call made while a pump runs is not dropped: that pump runs once more.
	if (isPumping) {
		isPumpWanted = true
		return
	}
	isPumping = true
	isPumpWanted = false
	let hasFailed = false
	try {
		for (const id of await get($, led)) {
			// A room that fails is tried again later; the rooms after it go on.
			try {
				await pumpRoom($, id)
			} catch {
				hasFailed = true
			}
		}
	} catch {
		hasFailed = true
	} finally {
		isPumping = false
	}
	if (hasFailed) $.clock.after(3000, () => void pump($))
	if (isPumpWanted) void pump($)
}

// pumpRoom puts what is new for main in led room id into the chat, then
// tells main that the room ended once it has.
async function pumpRoom($: Engine, id: string) {
	// watch can print a room's end before its last messages: read the
	// whole transcript once the room has ended, before the end note.
	const hasEnded = Boolean((await get($, rooms))[id]?.ended_at)
	if (hasEnded && !(await readLog($, id))) throw new Error("no log")
	for (;;) {
		const pending = after(
			forMain((await get($, messages))[id] ?? []),
			(await get($, delivered))[id] ?? null,
		)
		const batch = pending.slice(0, 16)
		const last = batch[batch.length - 1]
		if (!last) break
		if (!(await deliver($, id, batch))) throw new Error("not delivered")
		await put($, delivered, (all) => ({
			...all,
			[id]: { id: last.id, at: last.at },
		}))
		await ack($, id, last)
	}
	// Ack the saved mark too: an ack that failed before a reload is
	// tried again here.
	await ack($, id, (await get($, delivered))[id] ?? null)
	const room = (await get($, rooms))[id]
	if (hasEnded && room) {
		if (!(await get($, notified)).includes(id)) {
			const reason = room.ended_reason ? ` (${room.ended_reason})` : ""
			const note = `peer room ${id} has ended${reason}. Its members no longer read or answer messages.`
			if (!(await deliver($, id, [], note))) throw new Error("not delivered")
			await put($, notified, (list) => [...list, id])
		}
		await put($, led, (list) => list.filter((one) => one !== id))
	}
}

// ack moves main's cursor just past mark, the last message in the chat, so
// unread counts stay right and a wait run after the plugin is turned off
// misses nothing. A failed ack throws, and the pump tries again later.
async function ack($: Engine, id: string, mark: PeerMark | null) {
	if (!mark) return
	const { exitCode, stderr } = await peer($, [
		"ack",
		id,
		"--as",
		"main",
		"--upto",
		mark.id,
	])
	if (exitCode !== 0) {
		// An old peer CLI has no ack and prints its usage.
		const reason = /usage|unknown/i.test(stderr)
			? "update the peer CLI (peer update or brew upgrade --cask peer)"
			: stderr.trim().slice(0, 200) || `exit code ${exitCode}`
		const text = `peer ack failed for ${id}: ${reason}`
		await put($, ackErrors, (all) =>
			all[id] === text ? all : { ...all, [id]: text },
		)
		throw new Error("not acked")
	}
	await put($, ackErrors, (all) => {
		if (!(id in all)) return all
		const { [id]: _, ...rest } = all
		return rest
	})
}

// deliver queues one batch, or a note, as a turn of its own: the session
// runs it once it is idle, so no message lands in a turn that is ending.
// It says whether the turn was queued.
async function deliver(
	$: Engine,
	id: string,
	list: PeerMessage[],
	note?: string,
) {
	const body = list
		.map(
			(m) =>
				`[${m.from} → ${m.to === EVERYONE ? "all" : m.to} · ${clock(m.at, true)}]\n${m.text}`,
		)
		.join("\n\n")
	const text =
		note ??
		`peer room ${id}: ${list.length} new message${list.length === 1 ? "" : "s"} for main, delivered by the peer plugin; you do not run peer wait for this room.\n\n${body}`
	deliveries.set(text, deliverySummary(id, list, Boolean(note)))
	const submitted = await $.prompt.submit({ text })
	const isQueued = !("drop" in submitted && submitted.drop)
	if (!isQueued) deliveries.delete(text)
	return isQueued
}

// refresh keeps the footer's count and the shown log or boards tab current
// while the pane is placed.
async function refresh($: Engine) {
	const pane = (await $.ui.panes()).find(
		(one) => one.id === PANE && one.isPlaced,
	)
	if (!pane) return
	const st = await snapshot($)
	const room = current(st)
	if (!room) return
	// What the pane shows counts as read for the footer's count.
	const count = (st.messages[room.id] ?? []).length
	if (pane.isShown && st.seen[room.id] !== count)
		await put($, seen, (all) => ({ ...all, [room.id]: count }))
	const shown = await get($, tab)
	if (shown === BOARDS) await loadBoards($, room)
	else if (shown !== "chat") await loadLog($, room)
}

// loadBoards lists the boards that room's commands used, from peer board
// list in its checkout, and reads each board.json whose mtime or revision
// changed since the last read.
async function loadBoards($: Engine, room: PeerRoom) {
	const { exitCode, stdout, stderr } = await $.process.run(
		["peer", "board", "list"],
		{ cwd: room.repo, timeoutMs: 30_000 },
	)
	if (exitCode !== 0) {
		const error = /usage|unknown/i.test(stderr)
			? "peer board failed: update the peer CLI (peer update or brew upgrade --cask peer)"
			: `peer board list failed: ${stderr.trim().slice(0, 200)}`
		boardViews = { ...boardViews, [room.id]: { boards: [], error } }
		$.ui.invalidate("ui.render")
		return
	}
	let refs: PeerBoardRef[] = []
	try {
		refs = (JSON.parse(stdout) as { boards?: PeerBoardRef[] }).boards ?? []
	} catch {
		// an empty list
	}
	const before = boardViews[room.id]?.boards ?? []
	const boards: BoardView[] = []
	for (const ref of refs.filter((one) => one.rooms?.includes(room.id))) {
		const old = before.find((view) => view.ref.path === ref.path)
		// mtime has whole seconds: revision tells two writes in one second apart.
		const isSame =
			old?.board &&
			old.ref.mtime === ref.mtime &&
			old.ref.revision === ref.revision
		if (ref.error)
			boards.push({ ref, error: ref.error.message ?? ref.error.error })
		else if (isSame) boards.push({ ...old, ref })
		else boards.push(await readBoard($, ref))
	}
	boardViews = { ...boardViews, [room.id]: { boards } }
	$.ui.invalidate("ui.render")
}

async function readBoard($: Engine, ref: PeerBoardRef): Promise<BoardView> {
	try {
		const text = await $.fs.read(`${ref.path}/board.json`)
		return { ref, board: JSON.parse(text as string) as PeerBoard }
	} catch (err) {
		return {
			ref,
			error: `cannot read board.json: ${String(err).slice(0, 200)}`,
		}
	}
}

// loadLog reads the log of the member whose tab is shown.
async function loadLog($: Engine, room: PeerRoom) {
	const role = await get($, tab)
	if (role === "chat") return
	const { exitCode, stdout } = await peer($, ["memberlog", room.id, role])
	if (exitCode !== 0) return
	const entries: PeerLogEntry[] = []
	for (const line of stdout.split("\n")) {
		try {
			if (line.trim()) entries.push(JSON.parse(line))
		} catch {
			// skip a line that is not JSON
		}
	}
	await put($, log, () => entries.slice(-200))
}

function state(m: PeerMember) {
	if (m.kicked) return "kicked"
	if (m.exited) return "exited"
	if (m.pid) return "running"

	return m.role === "main" ? "leads" : "joined"
}

export const register: Register = (on) => {
	on("session.start", async ($, e, next) => {
		const started = await next(e)
		await begin($)
		return started
	})

	on("turn.start", async ($, e, next) => {
		await begin($)
		if (startingTask) isStartingTurn = true
		// A delivery's turn names what it reads on the spinner.
		const queued = [...deliveries.keys()].find((text) => e.text.includes(text))
		const note = queued === undefined ? null : deliveries.get(queued)
		if (queued !== undefined) deliveries.delete(queued)
		const line = note?.line ?? ""
		deliveryAuthor = note?.author ?? ""
		if (line !== deliveryNote) {
			deliveryNote = line
			$.ui.invalidate("ui.render")
		}
		return next(e)
	})

	// The turn that ran the pane's /peer:peer is over: a room it started has
	// cleared the task already, else the field shows again. After a
	// delivery, the empty prompt box suggests telling its last author.
	on("turn.complete", async ($, e, next) => {
		if (isStartingTurn) starting($, "")
		const done = await next(e)
		// A subagent's turn ends inside the delivery's turn.
		if (e.agentId) return done
		const author = deliveryAuthor
		if (deliveryNote) {
			deliveryNote = ""
			$.ui.invalidate("ui.render")
		}
		deliveryAuthor = ""
		if (author)
			void $.prompt.suggest({ text: `Tell ${author}: ` }).catch(() => ({}))
		return done
	})

	on("ui.render", { component: "Spinner" }, (_, e, next) =>
		deliveryNote
			? next({ ...e, props: { ...e.props, message: deliveryNote } })
			: next(e),
	)

	// /room opens or closes the pane, as the footer's button does.
	on("command.run", { command: "room" }, async ($) => {
		await begin($)
		const done = await togglePane($)
		if (done !== "not placed") return { text: `peer: pane ${done}` }
		return { text: "peer: there is no room for the pane here" }
	})

	// @ROLE in the prompt box offers the members of the room the pane shows.
	on("prompt.autocomplete", { token: /^@/ }, async ($, e, next) => {
		const found = await next(e)
		const room = current(await snapshot($))
		if (!room || room.ended_at) return found
		const prefix = e.token.slice(1)
		const roles = room.members
			.filter(
				(m) => m.role !== "main" && !m.kicked && m.role.startsWith(prefix),
			)
			.map((m) => ({
				text: `@${m.role}`,
				description: `${m.worker ? "worker" : "member"} of ${room.id}`,
			}))
		return { suggestions: [...found.suggestions, ...roles] }
	})

	on("tool.call", { tool: "Bash" }, async ($, e, next) => {
		await begin($)
		const all = shellCommands(e.command)
		const commands = all
			.map(peerArgs)
			.filter((args): args is string[] => args !== null)
		const wait = commands.find((args) => args[0] === "wait" && asMain(args))
		if (wait?.[1] && all.length > 1 && (await get($, led)).includes(wait[1])) {
			// Answering the whole line would skip its other commands.
			return { deny: `Run the other commands without peer wait. ${HANDOFF}` }
		}
		if (wait?.[1] && all.length === 1) {
			const id = wait[1]
			const room = (await get($, rooms))[id]
			// led first: rooms comes from watch and can lag behind a start.
			if ((await get($, led)).includes(id) || (room && !room.ended_at)) {
				await lead($, id)
				return {
					result: {
						stdout: JSON.stringify({ status: "timeout", note: HANDOFF }),
						stderr: "",
						interrupted: false,
					},
				}
			}
			return next(e)
		}
		// The permission dialog of a peer end or kick says who it stops.
		const stop = commands.find(
			(args) => (args[0] === "end" || args[0] === "kick") && args[1],
		)
		const effect = stop && stopNote(roomIndex[stop[1] ?? ""], stop)
		if (effect) {
			try {
				$.ui.notice(e.tool_use_id, effect)
			} catch {
				// no dialog for this call
			}
		}
		const startedAt = new Date(Date.now() - 1000).toISOString()
		const ran = await next(e)
		if (ran.deny !== undefined || ran.isError) return ran
		// Main ended the room itself, so it needs no note that it ended.
		for (const args of commands) {
			if (args[0] === "end" && args[1] && asMain(args))
				await put($, notified, (list) => [...list, args[1] ?? ""])
		}
		// A room this session joins as a member is its own too: a peer join on
		// a line of its own that printed the room with that role. On a longer
		// line, another command can print the room after a failed join.
		const stdout = (ran.result as { stdout?: string } | undefined)?.stdout ?? ""
		const [cmd, id, role] = commands[0] ?? []
		if (
			all.length === 1 &&
			cmd === "join" &&
			id &&
			role &&
			joinedRoom(stdout, id, role)
		) {
			await put($, own, (list) => (list.includes(id) ? list : [...list, id]))
		}
		const start = commands.find((args) => args[0] === "start" && args[1])
		if (!start) return ran
		const room =
			(await startedRoom(stdout)) ??
			(await newRoom($, start[1] ?? "", startedAt))
		if (!room) return ran
		await put($, selected, () => room.id)
		await lead($, room.id, true)
		starting($, "")
		void openPane($)
		return { ...ran, context: [HANDOFF] }
	})

	on("ui.render", { component: "Pane", requestId: PANE }, async ($, e) => {
		isPaneFocused = e.props.isFocused
		const st = await snapshot($)
		const els = $.ui.resolve(e)
		const { Box, Text, Button, Markdown, Code } = els
		const room = current(st)
		// starter starts a new room with the plugin's skill: the person writes
		// the task in the field and presses Enter, and the field gives way to
		// the task until its room starts. A surface without Input gets a
		// button that puts /peer:peer in the prompt box instead. The padding
		// leaves room for the field's focus ring.
		let starter = (
			<Text dimColor>Starting a room: {clip(startingTask, 200)}</Text>
		)
		if (!startingTask)
			starter =
				"Input" in els ? (
					<Box paddingY="xs" paddingX="xs">
						<els.Input
							key="new-room"
							label="new room"
							placeholder="task"
							submitLabel="start"
							autoFocus
							onSubmit={(task) => {
								if (!task.trim()) return
								isComposing = false
								starting($, task.trim())
								$.command
									.run({ command: SKILL, args: task.trim() })
									.catch((err) => {
										starting($, "")
										$.ui.toast(`peer: ${String(err).slice(0, 200)}`)
									})
							}}
						/>
					</Box>
				) : (
					<Box>
						<Button
							key="new-room"
							label="+ new room"
							hotkey="n"
							variant="secondary"
							onPress={async () => {
								const { isFilled } = await $.prompt.fill({ text: `/${SKILL} ` })
								if (!isFilled) $.ui.toast(`peer: type /${SKILL} TASK`)
							}}
						/>
					</Box>
				)
		if (!room) {
			return (
				<Box flexDirection="column" gap={1}>
					<Text dimColor>
						No active peer room in this checkout. Start one with /peer:peer
						TASK.
					</Text>
					{starter}
					{st.watchError && <Text color="red">{st.watchError}</Text>}
					{Object.entries(st.ackErrors).map(([id, text]) => (
						<Text key={`ack-${id}`} color="red">
							{text}
						</Text>
					))}
				</Box>
			)
		}
		const active = activeRooms(st)
		const isOpen = !room.ended_at
		const all = st.messages[room.id] ?? []
		// A kicked member keeps its tab, ○, for its log.
		const others = room.members.filter((m) => m.role !== "main")
		// A member's tab from another room shows this room's chat.
		const isTab = st.tab === BOARDS || others.some((m) => m.role === st.tab)
		const shown = isTab ? st.tab : "chat"
		const pending = st.confirm

		// armed draws a button that acts on its second press: the first turns
		// it into confirm, and three seconds without the second turn it back.
		const armed = (
			what: string,
			key: string,
			label: string,
			hotkey: string,
			argv: string[],
		) =>
			pending === what ? (
				<Button
					key={key}
					label="confirm"
					hotkey={hotkey}
					variant="primary"
					onPress={async () => {
						await put($, confirm, () => null)
						const { exitCode, stderr } = await peer($, argv)
						if (exitCode !== 0) $.ui.toast(`peer: ${stderr.trim()}`)
					}}
				/>
			) : (
				<Button
					key={key}
					label={label}
					hotkey={hotkey}
					variant="secondary"
					onPress={async () => {
						await put($, confirm, () => what)
						$.clock.after(
							3000,
							() => void put($, confirm, (now) => (now === what ? null : now)),
						)
					}}
				/>
			)

		// One row: the chat, the boards, a tab per member and, at the right, the
		// new room and end buttons; the new room button shows the field. A member's tab shows its log.
		const pick = (one: string) => async () => {
			replay = null
			await put($, tab, () => one)
			if (one === "chat") toNewest($)
			else if (one === BOARDS) await loadBoards($, room)
			else await loadLog($, room)
		}
		// Every tab is a button of one size, so a switch moves nothing; the
		// dot is part of the label, so it is pressed with the rest: ● runs, ○
		// has stopped, as every member of an ended room has.
		const dot = (isOn: boolean) => (isOn ? "●" : "○")
		const boardList = boardViews[room.id]
		const header = (
			<Box flexDirection="column" gap={1}>
				{active.length > 1 && "Select" in els && (
					<els.Select
						key="room"
						label="room"
						value={room.id}
						options={active.map((one) => ({ value: one.id }))}
						onSelect={(value) => put($, selected, () => value)}
					/>
				)}
				<Box gap={1} flexWrap="wrap">
					<Button
						key="tab-chat"
						label={`${dot(isOpen)} chat`}
						hotkey="1"
						variant={shown === "chat" ? "primary" : "secondary"}
						onPress={pick("chat")}
					/>
					<Button
						key="tab-boards"
						label="boards"
						hotkey="2"
						variant={shown === BOARDS ? "primary" : "secondary"}
						onPress={pick(BOARDS)}
					/>
					{others.map((m, i) => (
						<Button
							key={`tab-${m.role}`}
							label={`${dot(isOpen && !m.exited && !m.kicked)} ${m.role}`}
							hotkey={i < 7 ? String(i + 3) : undefined}
							variant={shown === m.role ? "primary" : "secondary"}
							onPress={pick(m.role)}
						/>
					))}
					<Box flexGrow={1} />
					{"Input" in els && (
						<Button
							key="new-room-open"
							label="+ new room"
							hotkey="n"
							variant={isComposing ? "primary" : "secondary"}
							onPress={() => {
								isComposing = !isComposing
								$.ui.invalidate("ui.render")
							}}
						/>
					)}
					{isOpen &&
						armed("end", "end", "⏹ end", "e", ["end", room.id, "--as", "user"])}
				</Box>
				{(isComposing || startingTask || !("Input" in els)) && starter}
				{st.watchError && <Text color="red">{st.watchError}</Text>}
				{Object.entries(st.ackErrors).map(([id, text]) => (
					<Text key={`ack-${id}`} color="red">
						{text}
					</Text>
				))}
			</Box>
		)

		let body: JSX.Element
		if (shown === "chat") {
			// Newest first: the chat grows down from the tabs.
			const list = all.slice(-60).reverse()
			body = (
				<Box flexDirection="column" gap={1}>
					{e.surface !== "terminal" && "Svg" in els && list.length > 0 && (
						<els.Svg
							source={timeline(room, list)}
							alt={`Message timeline of ${room.id}`}
						/>
					)}
					{list.length === 0 && <Text dimColor>No messages yet.</Text>}
					{groups(list).map((group) =>
						group.notices ? (
							// One line reads in time order, though the lines run newest first.
							<Box key={`notice-${group.notices[0]?.id}`} gap={1}>
								<Text dimColor>
									{clock(group.notices[group.notices.length - 1]?.at ?? "")}
								</Text>
								<Text dimColor>
									{clip(
										[...group.notices]
											.reverse()
											.map((m) => m.text)
											.join(" · "),
										2000,
									)}
								</Text>
							</Box>
						) : (
							message(group.message)
						),
					)}
				</Box>
			)
		} else if (shown === BOARDS) {
			body = (
				<Box flexDirection="column" gap={1}>
					{boardList?.error && <Text color="red">{boardList.error}</Text>}
					{boardList?.boards.length === 0 && (
						<Text dimColor>
							No boards in this room yet. Agents make one with peer board
							create.
						</Text>
					)}
					{boardList?.boards.map((view) => boardCard(view))}
					{(boardList?.boards.length ?? 0) > 0 && (
						<Text dimColor>If a link does not open, start `peer`.</Text>
					)}
				</Box>
			)
		} else {
			const member = room.members.find((m) => m.role === shown)
			const steps = editSteps(st.log)
			const isReplaying = replay?.role === shown && steps.length > 0
			body = (
				<Box flexDirection="column" gap={1}>
					{member && (
						<Box gap={1}>
							<Text dimColor>
								{[member.agent, member.model, member.effort]
									.filter(Boolean)
									.join(" / ") || "joined by hand"}{" "}
								· {state(member)}
								{member.worktree ? ` · worktree ${member.worktree}` : ""}
								{member.branch ? ` · ${member.branch}` : ""}
							</Text>
							<Box flexGrow={1} />
							{steps.length > 0 && (
								<Button
									key="replay"
									label={isReplaying ? "log" : `replay ${steps.length} edits`}
									hotkey="r"
									variant={isReplaying ? "primary" : "secondary"}
									onPress={() => {
										replay = isReplaying ? null : { role: shown, index: 0 }
										$.ui.invalidate("ui.render")
									}}
								/>
							)}
							{isOpen &&
								!member.kicked &&
								armed(`kick:${member.role}`, "kick", "kick", "k", [
									"kick",
									room.id,
									member.role,
									"--as",
									"user",
								])}
						</Box>
					)}
					{isReplaying && member && replayView(room, member, steps)}
					{st.log.length === 0 && <Text dimColor>No log yet.</Text>}
					{!isReplaying &&
						st.log
							.slice(-80)
							.reverse()
							.map((entry, i) => (
								// biome-ignore lint/suspicious/noArrayIndexKey: log entries have no id; the list is redrawn whole.
								<Box key={`log-${i}`} gap={1}>
									<Text dimColor>
										{entry.at && !entry.at.startsWith("0001")
											? clock(entry.at, true)
											: "        "}
									</Text>
									<Box flexGrow={1} flexShrink={1}>
										{entry.kind === "tool" ? (
											<Code source={clip(entry.text, 500)} language="bash" />
										) : (
											<Text
												dimColor={entry.kind !== "text"}
												wrap={entry.kind === "text" ? "wrap" : "truncate-end"}
											>
												{clip(
													entry.kind === "text"
														? entry.text
														: entry.text.split("\n").slice(0, 3).join(" ⏎ "),
													500,
												)}
											</Text>
										)}
									</Box>
								</Box>
							))}
				</Box>
			)
		}

		// replayView steps through a member's edits one at a time: a strip of
		// the steps, the file, and the file's diff in the member's checkout
		// now, since its log keeps the edit's path, not its text.
		function replayView(
			shownRoom: PeerRoom,
			member: PeerMember,
			steps: EditStep[],
		) {
			const k = Math.max(0, Math.min(replay?.index ?? 0, steps.length - 1))
			const step = steps[k] as EditStep
			const dir = member.worktree || shownRoom.repo
			const path = step.path.startsWith(`${dir}/`)
				? step.path.slice(dir.length + 1)
				: step.path
			const key = `${dir}\n${step.path}`
			if (replayDiff?.key !== key) void loadDiff($, key, dir, step.path)
			const diff = replayDiff?.key === key ? replayDiff : null
			const lines = (diff?.text ?? "")
				.split("\n")
				.filter(
					(line) => line && !/^(diff --git|index |--- |\+\+\+ )/.test(line),
				)
			const go = (to: number) => () => {
				replay = {
					role: member.role,
					index: Math.max(0, Math.min(to, steps.length - 1)),
				}
				$.ui.invalidate("ui.render")
			}
			return (
				<Box key="replay-view" flexDirection="column">
					<Box gap={1} flexWrap="wrap">
						{steps.map((_, n) => (
							<Text
								// biome-ignore lint/suspicious/noArrayIndexKey: steps are positions in the log.
								key={`step-${n}`}
								inverse={n === k}
								dimColor={n !== k}
							>
								{` ${n + 1} `}
							</Text>
						))}
					</Box>
					<Box gap={1}>
						<Text bold color={authorColor(shownRoom, member.role)}>
							{path}
						</Text>
						<Text dimColor>
							{`${step.tool}${step.at ? ` · ${clock(step.at, true)}` : ""} · step ${k + 1} of ${steps.length}`}
						</Text>
					</Box>
					<Text dimColor>
						The file's uncommitted diff now, not this step's own.
					</Text>
					{diff?.error && <Text color="red">{diff.error}</Text>}
					{diff?.isLoaded && !diff.error && lines.length === 0 && (
						<Text dimColor>
							No uncommitted change: committed, untracked or undone.
						</Text>
					)}
					{lines.slice(0, 12).map((line, n) => (
						<Text
							// biome-ignore lint/suspicious/noArrayIndexKey: diff lines repeat.
							key={`diff-${n}`}
							color={DIFF_COLOR[line[0] ?? ""]}
							dimColor={!/^[+-]/.test(line)}
							wrap="truncate-end"
						>
							{clip(line, 300)}
						</Text>
					))}
					{lines.length > 12 && (
						<Text dimColor>… {lines.length - 12} more lines</Text>
					)}
					<Box gap={1}>
						<Button
							key="replay-prev"
							label="◀ prev"
							hotkey="h"
							onPress={go(k - 1)}
						/>
						<Button
							key="replay-next"
							label="next ▶"
							hotkey="l"
							onPress={go(k + 1)}
						/>
					</Box>
				</Box>
			)
		}

		// boardCard shows a board's title and link, then a schematic where the
		// surface draws Svg, else its frames as text.
		function boardCard({ ref, board, error }: BoardView) {
			const isDrawn = e.surface !== "terminal" && "Svg" in els
			const svg = board && isDrawn ? boardSvg(board) : null
			const lines = board && !isDrawn ? frameLines(board) : []
			const raster =
				board && !isDrawn && "Raster" in els
					? boardRaster(
							board,
							Math.max(20, Math.min(72, e.props.bodyColumns - 4)),
						)
					: null
			return (
				<Box key={`board-${ref.key}`} flexDirection="column">
					<Text bold>
						<els.Link
							href={ref.url}
							label={`${board?.title ?? ref.title ?? ref.path} ↗`}
						/>
					</Text>
					{board && <Text dimColor>{boardStats(board)}</Text>}
					{error && <Text color="red">{error}</Text>}
					{svg && "Svg" in els && (
						<els.Svg
							source={svg}
							alt={`Schematic of board ${ref.title ?? ""}`}
						/>
					)}
					{raster && "Raster" in els && (
						<els.Raster key={`raster-${ref.key}`} {...raster} />
					)}
					{lines.map((line) => (
						<Text key={`${ref.key}-${line}`}>{line}</Text>
					))}
				</Box>
			)
		}

		function message(m: PeerMessage) {
			const color = authorColor(room!, m.from)
			const lines = m.text.split("\n")
			const isLong = lines.length > 14 && !st.expanded.includes(m.id)
			const text = isLong ? excerpt(lines, 12) : m.text
			return (
				<Box
					key={`msg-${m.id}`}
					flexDirection="column"
					borderStyle="single"
					borderColor={borderOf(room!, m.from)}
					paddingX={1}
				>
					<Box gap={1}>
						<Text dimColor>{clock(m.at)}</Text>
						<Text bold color={color}>
							{m.from}
						</Text>
						<Text dimColor>→</Text>
						<Text
							color={m.to === EVERYONE ? undefined : authorColor(room!, m.to)}
						>
							{m.to === EVERYONE ? "all" : m.to}
						</Text>
					</Box>
					<Markdown key={`md-${m.id}`} text={clip(linkify(text), 8000)} />
					{lines.length > 14 && (
						<Button
							key={`more-${m.id}`}
							label={isLong ? `show all ${lines.length} lines` : "show less"}
							plain
							onPress={() =>
								put($, expanded, (list) =>
									isLong ? [...list, m.id] : list.filter((one) => one !== m.id),
								)
							}
						/>
					)}
				</Box>
			)
		}

		return (
			<Box flexDirection="column" gap={1}>
				{header}
				{body}
			</Box>
		)
	})

	// A Bash row that only runs peer send is drawn as the message it sent, a
	// card in its author's color; any other row, or one that failed, as the
	// engine draws it.
	on("ui.render", { component: "ToolUse" }, ($, e, next) => {
		const { tool, input, isErrored, isInterrupted, isRunning } = e.props
		const command = (input as { command?: unknown } | undefined)?.command
		if (
			tool !== "Bash" ||
			isErrored ||
			isInterrupted ||
			typeof command !== "string"
		)
			return next(e)
		const sent = sentMessage(command)
		if (!sent) return next(e)
		const room = roomIndex[sent.id] ?? {
			id: sent.id,
			repo: "",
			members: [],
			started_at: "",
		}
		const { Box, Text, Markdown } = $.ui.resolve(e)
		const lines = sent.text.split("\n")
		const text = lines.length > 14 ? excerpt(lines, 12) : sent.text
		return (
			<Box
				flexDirection="column"
				borderStyle="single"
				borderColor={borderOf(room, sent.from)}
				paddingX={1}
			>
				<Box gap={1}>
					<Text dimColor>{`${ICON} ${sent.id}`}</Text>
					<Text bold color={authorColor(room, sent.from)}>
						{sent.from}
					</Text>
					<Text dimColor>→</Text>
					<Text
						color={
							sent.to === EVERYONE ? undefined : authorColor(room, sent.to)
						}
					>
						{sent.to === EVERYONE ? "all" : sent.to}
					</Text>
					{isRunning && <Text dimColor>sending…</Text>}
				</Box>
				<Markdown text={clip(linkify(text), 8000)} />
			</Box>
		)
	})

	// The prompt footer always gets one peer button, with the count of
	// messages in the session's rooms that came since the pane last showed
	// them: it opens the pane, and closes it when the pane already shows.
	on("ui.render", { component: "SessionMode" }, async ($, e) => {
		const st = await snapshot($)
		const { Box, Text, Button } = $.ui.resolve(e)
		const fresh = unseen(st)
		return (
			<Box gap={1}>
				{e.props.modes.length > 0 && (
					<Text dimColor>{e.props.modes.join(" & ")}</Text>
				)}
				<Button
					key="peer-open"
					label={`${ICON} Peer${fresh > 0 ? ` ${fresh}` : ""}`}
					plain
					onPress={() => togglePane($)}
				/>
			</Box>
		)
	})

	// The rows this plugin puts into the chat, a delivery or an end
	// note, are hidden there: the toast, the pane and the footer's count show
	// them. ctrl+o shows each as the model reads it. A surface without ctrl+o,
	// such as the desktop, marks every row expanded, so it always hides them.
	on("ui.render", { component: "UserMessage" }, ($, e, next) => {
		const hasCtrlO = e.surface === "terminal" || e.surface === "vscode"
		if ((hasCtrlO && e.props.isExpanded) || !isPeerRow(e.props.text))
			return next(e)
		const { Box } = $.ui.resolve(e)
		return <Box />
	})
}

// sentMessage reads a line that only runs peer send with --text: its
// room, author, recipient and text. A line the shell would change first,
// with $, `, \, < or >, is not read: a card must show what was sent.
function sentMessage(command: string) {
	if (/[$`\\<>]/.test(command)) return null
	const all = shellCommands(command)
	const args = all.length === 1 ? peerArgs(all[0] ?? []) : null
	if (args?.[0] !== "send" || !args[1]) return null
	const sent = { id: args[1], from: "", to: EVERYONE, text: "" }
	let hasText = false
	for (let i = 2; i < args.length; i++) {
		const arg = args[i] ?? ""
		const [flag, inline] = arg.startsWith("--") ? arg.split(/=(.*)/s) : [arg]
		const value = inline ?? args[++i]
		if (value === undefined) return null
		if (flag === "--as") sent.from = value
		else if (flag === "--to") sent.to = value
		else if (flag === "--text") {
			sent.text = value
			hasText = true
		} else return null
	}
	return sent.from && hasText ? sent : null
}

// DIFF_COLOR colors a diff line by its first character.
const DIFF_COLOR: Record<string, string> = { "+": "green", "-": "red" }

type EditStep = { path: string; tool: string; at?: string }

// editSteps lists a member's file edits from its log: Claude's Edit,
// Write and MultiEdit with their file_path, read only when the logged input
// is whole JSON (the log cuts it at 200 characters), and Codex's edit with
// its paths.
function editSteps(entries: PeerLogEntry[]) {
	const steps: EditStep[] = []
	for (const entry of entries) {
		if (entry.kind !== "tool") continue
		const claude = /^(Edit|Write|MultiEdit) (\{[\s\S]*\})$/.exec(entry.text)
		if (claude) {
			try {
				const path = (JSON.parse(claude[2] ?? "") as { file_path?: unknown })
					.file_path
				if (typeof path === "string" && path)
					steps.push({ path, tool: claude[1] ?? "", at: entry.at })
			} catch {
				// cut input: its path may be cut too
			}
			continue
		}
		const codex = /^edit (.+)$/.exec(entry.text)
		for (const path of codex?.[1]?.split(" ").filter(Boolean) ?? [])
			steps.push({ path, tool: "edit", at: entry.at })
	}
	return steps
}

// loadDiff reads path's uncommitted diff in the checkout dir for the step
// key; a path outside it shows git's error.
async function loadDiff($: Engine, key: string, dir: string, path: string) {
	replayDiff = { key, text: "" }
	try {
		const { exitCode, stdout, stderr } = await $.process.run(
			["git", "-C", dir, "diff", "--", path],
			{ timeoutMs: 10_000 },
		)
		if (replayDiff?.key !== key) return
		replayDiff =
			exitCode === 0
				? { key, text: stdout, isLoaded: true }
				: {
						key,
						text: "",
						error: `git diff: ${clip(stderr.trim(), 200)}`,
						isLoaded: true,
					}
	} catch (err) {
		if (replayDiff?.key === key)
			replayDiff = {
				key,
				text: "",
				error: `git diff: ${clip(String(err), 200)}`,
				isLoaded: true,
			}
	}
	$.ui.invalidate("ui.render")
}

// deliverySummary is what a delivery's turn reads, from its messages'
// from fields: one line for the spinner and the last member that wrote,
// a valid role name, for the prompt box's suggestion.
function deliverySummary(id: string, list: PeerMessage[], hasEnded: boolean) {
	if (hasEnded) return { line: `peer: room ${id} ended`, author: "" }
	const names = [
		...new Set(
			list
				.map((m) => m.from)
				.filter(
					(one) =>
						one !== "peer" &&
						one !== "user" &&
						/^[a-z][a-z0-9_-]{0,63}$/.test(one),
				),
		),
	]
	const n = list.length
	return {
		line: `peer: reading ${n} message${n === 1 ? "" : "s"}${names.length > 0 ? ` from ${names.join(", ")}` : ""}`,
		author: names.at(-1) ?? "",
	}
}

// stopNote says what a peer end or kick stops: the members of room whose
// agents still run, or the kicked member's agent.
function stopNote(room: PeerRoom | undefined, args: string[]) {
	if (!room || room.ended_at) return ""
	const running = room.members.filter(
		(m) => m.role !== "main" && m.pid && !m.exited && !m.kicked,
	)
	if (args[0] === "end") {
		const names = running.map((m) => m.role)
		return names.length > 0
			? `peer: ends ${room.id}; still running: ${names.join(", ")}`
			: `peer: ends ${room.id}`
	}
	const member = room.members.find((m) => m.role === args[2])
	if (!member) return ""
	const agent = [member.agent, member.model].filter(Boolean).join(" ")
	return `peer: removes ${member.role}${agent ? ` (${agent})` : ""} from ${room.id}${running.includes(member) ? " and stops its agent" : ""}`
}

// startedRoom reads the room peer start printed, when the output reached
// the tool whole.
async function startedRoom(stdout: string) {
	try {
		const room = JSON.parse(stdout.trim().split("\n")[0] ?? "") as PeerRoom
		return room.id && room.members?.[0]?.role === "main" ? room : null
	} catch {
		return null
	}
}

// joinedRoom reports whether stdout holds room id as peer join prints it,
// with role among its members.
function joinedRoom(stdout: string, id: string, role: string) {
	return stdout.split("\n").some((line) => {
		try {
			const room = JSON.parse(line) as PeerRoom
			return room.id === id && room.members?.some((m) => m.role === role)
		} catch {
			return false
		}
	})
}

// newRoom finds the room a peer start NAME made when its output was cut or
// piped away: NAME, or NAME-2 and so on, started since the command ran.
async function newRoom($: Engine, name: string, since: string) {
	const { exitCode, stdout } = await peer($, ["status"])
	if (exitCode !== 0) return null
	const rooms: PeerRoom[] = []
	for (const line of stdout.split("\n")) {
		try {
			if (line.trim()) rooms.push(JSON.parse(line))
		} catch {
			// skip a line that is not JSON
		}
	}
	const named = new RegExp(`^${name.replace(/[^a-z0-9-]/g, "")}(-\\d+)?$`)
	return (
		rooms
			.filter((room) => named.test(room.id) && room.started_at >= since)
			.sort((a, b) => b.started_at.localeCompare(a.started_at))[0] ?? null
	)
}

// shellCommands splits a shell command line into its commands, each a list
// of words with quotes removed and leading VAR=value words dropped; text
// inside quotes, as in echo "peer wait", is not a command. It reads simple
// command lines, not every shell form.
function shellCommands(line: string): string[][] {
	const commands: string[][] = []
	let words: string[] = []
	let word = ""
	let hasWord = false
	let quote = ""
	const endWord = () => {
		if (hasWord) words.push(word)
		word = ""
		hasWord = false
	}
	const endCommand = () => {
		endWord()
		const at = words.findIndex((w) => !/^[A-Za-z_][A-Za-z0-9_]*=/.test(w))
		if (at >= 0) commands.push(words.slice(at))
		words = []
	}
	for (const c of line) {
		if (quote) {
			if (c === quote) quote = ""
			else word += c
		} else if (c === '"' || c === "'") {
			quote = c
			hasWord = true
		} else if (c === " " || c === "\t") {
			endWord()
		} else if (
			c === ";" ||
			c === "&" ||
			c === "|" ||
			c === "\n" ||
			c === "(" ||
			c === ")"
		) {
			endCommand()
		} else {
			word += c
			hasWord = true
		}
	}
	endCommand()
	return commands
}

// peerArgs returns a command's arguments when it runs peer.
function peerArgs(words: string[]) {
	const name = words[0] ?? ""
	return name === "peer" || name.endsWith("/peer") ? words.slice(1) : null
}

function asMain(args: string[]) {
	return args.some(
		(arg, i) =>
			arg === "--as=main" || (arg === "--as" && args[i + 1] === "main"),
	)
}

// authorColor gives each participant the color of its kind: main, the
// user, peer's notices, workers and the other members.
function authorColor(room: PeerRoom, name: string) {
	if (name === "main") return "#4c8dff"
	if (name === "user") return "#e5c07b"
	if (name === "peer") return "gray"
	return room.members.find((m) => m.role === name)?.worker
		? "#ff9f43"
		: "#b48cff"
}

// borderOf is a participant's color mixed into the dark background, so a
// card's frame tells its author without drawing the eye.
function borderOf(room: PeerRoom, name: string) {
	const color = authorColor(room, name)
	if (!color.startsWith("#")) return "#3a3a3a"
	const mix = (at: number) =>
		Math.round(Number.parseInt(color.slice(at, at + 2), 16) * 0.3 + 0x1b * 0.7)
	return `#${[1, 3, 5].map((at) => mix(at).toString(16).padStart(2, "0")).join("")}`
}

type Group =
	| { message: PeerMessage; notices?: undefined }
	| { notices: PeerMessage[]; message?: undefined }

// groups puts runs of peer's notices on one line between the messages.
function groups(list: PeerMessage[]): Group[] {
	const out: Group[] = []
	for (const m of list) {
		const last = out[out.length - 1]
		if (m.from !== "peer") out.push({ message: m })
		else if (last?.notices) last.notices.push(m)
		else out.push({ notices: [m] })
	}
	return out
}

// excerpt keeps the first n lines and closes a code fence left open.
function excerpt(lines: string[], n: number) {
	const head = lines.slice(0, n)
	const fences = head.filter((line) =>
		line.trimStart().startsWith("```"),
	).length
	return [...head, ...(fences % 2 === 1 ? ["```"] : []), "…"].join("\n")
}

const PATH_REF =
	/(?:\/|\.{1,2}\/)?(?:[\w.@-]+\/)*[\w@-][\w.@-]*\.[A-Za-z]\w*(?::\d+)?(?::\d+)?/g

// linkify turns references to the checkout's files, as file.go:42 or
// `file.go:42`, into file links; code blocks stay as written.
function linkify(text: string) {
	if (files.paths.size === 0) return text
	const link = (ref: string, shown: string) => {
		const path = ref.split(":")[0] ?? ""
		const rel = path.startsWith(`${files.repo}/`)
			? path.slice(files.repo.length + 1)
			: path.replace(/^\.\//, "")
		return files.paths.has(rel)
			? `[${shown}](file://${encodeURI(`${files.repo}/${rel}`)})`
			: shown
	}
	function linkSpan(span: string) {
		if (!span.startsWith("`"))
			return span.replace(PATH_REF, (ref) => link(ref, ref))

		const code = span.slice(1, -1)

		return new RegExp(`^${PATH_REF.source}$`).test(code)
			? link(code, span)
			: span
	}
	return text
		.split(/(```[\s\S]*?```)/)
		.map((part) =>
			part.startsWith("```")
				? part
				: part
						.split(/(`[^`\n]+`)/)
						.map(linkSpan)
						.join(""),
		)
		.join("")
}

// clock shows an RFC 3339 time in the host's time zone, as 15:04 or,
// with seconds, 15:04:05.
function clock(at: string, withSeconds = false) {
	const t = Date.parse(at)
	if (Number.isNaN(t)) return ""
	const d = new Date(t + utcOffset * 60_000)
	const pad = (n: number) => String(n).padStart(2, "0")
	return `${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}${withSeconds ? `:${pad(d.getUTCSeconds())}` : ""}`
}

// isPeerRow tells a row this plugin submitted by its text; the engine's
// framing may surround it.
function isPeerRow(text: string) {
	return /(?:^|\n)peer room \S+: \d+ new messages? for main, delivered by the peer plugin|(?:^|\n)peer room \S+ has ended\. Its members/.test(
		text,
	)
}

// clip cuts text to at most max characters: the desktop refuses a whole
// drawing with a text over 10,000 characters, and a member's log carries
// tool results of any size.
function clip(text: string, max: number) {
	return text.length > max ? `${text.slice(0, max - 1)}…` : text
}

// timeline draws one row per participant and one dot per message it sent.
function timeline(room: PeerRoom, list: PeerMessage[]) {
	const roles = [
		"main",
		...room.members.filter((m) => m.role !== "main").map((m) => m.role),
		"user",
		"peer",
	]
	const used = roles.filter(
		(role) => role === "main" || list.some((m) => m.from === role),
	)
	const start = Date.parse(room.started_at)
	const end = room.ended_at
		? Date.parse(room.ended_at)
		: Math.max(start + 1, ...list.map((m) => Date.parse(m.at)))
	const width = 560
	const left = 110
	const row = 18
	const height = used.length * row + 8
	const x = (at: string) =>
		left +
		((Date.parse(at) - start) / Math.max(1, end - start)) * (width - left - 10)
	const esc = (s: string) =>
		s.replace(/[<>&"]/g, (c) => `&#${c.charCodeAt(0)};`)
	const rows = used
		.map((role, i) => {
			const y = i * row + 12
			const dots = list
				.filter((m) => m.from === role)
				.map(
					(m) =>
						`<circle cx="${x(m.at).toFixed(1)}" cy="${y}" r="3.5" fill="${role === "peer" ? "#999" : authorColor(room, role)}"/>`,
				)
				.join("")
			return `<text x="0" y="${y + 4}" font-size="11" fill="#888">${esc(role)}</text><line x1="${left}" y1="${y}" x2="${width - 10}" y2="${y}" stroke="#8884"/>${dots}`
		})
		.join("")
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${width} ${height}" width="${width}" height="${height}" font-family="sans-serif">${rows}</svg>`
}
