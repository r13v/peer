// The peer extension for Copilot CLI. After main's `peer start`, it runs
// `peer forward --copilot-session` for the room and puts each batch it
// hands over into this session as a queued turn, then acks it; peer keeps
// the room's reader, cursor and end. A canvas shows the chat of the rooms
// this session leads. Diagnostics go to stderr: stdout is the CLI's
// JSON-RPC channel.
import { execFile, spawn } from "node:child_process"
import { createHash, randomBytes } from "node:crypto"
import {
	mkdirSync,
	readdirSync,
	readFileSync,
	renameSync,
	writeFileSync,
} from "node:fs"
import { createServer } from "node:http"
import { homedir } from "node:os"
import { dirname, join } from "node:path"
import { createInterface } from "node:readline"
import { fileURLToPath } from "node:url"
import { createCanvas, joinSession } from "@github/copilot-sdk/extension"

const here = dirname(fileURLToPath(import.meta.url))
const log = (...args) => console.error("peer:", ...args)

// A binding {repo, room} is known by its key, since rooms of different
// checkouts can share an ID.
const keyOf = (b) =>
	`${createHash("sha256").update(b.repo).digest("hex").slice(0, 16)}-${b.room}`
// forwards maps a binding's key to its forward child, and ready to the
// binding once that forward holds main's delivery.
const forwards = new Map()
const ready = new Map()

/** Runs `peer ARGS` with input on stdin and returns its stdout. */
function peer(args, input, cwd) {
	return new Promise((resolve, reject) => {
		const child = execFile(
			"peer",
			args,
			{ cwd, timeout: 10_000, maxBuffer: 8 << 20 },
			(err, stdout, stderr) =>
				err ? reject(new Error(stderr.trim() || err.message)) : resolve(stdout),
		)
		child.stdin.end(input ?? "")
	})
}

const dataDir = () =>
	process.env.COPILOT_PLUGIN_DATA ??
	join(
		process.env.COPILOT_HOME ?? join(homedir(), ".copilot"),
		"plugin-data",
		"peer",
	)

// One file per room, so bindings written at once never overwrite each other.
const bindingsDir = (sessionId) => join(dataDir(), "sessions", sessionId)

function bind(sessionId, room) {
	const dir = bindingsDir(sessionId)
	mkdirSync(dir, { recursive: true })
	const b = { repo: room.repo, room: room.id }
	// Written aside and renamed, so a crash never leaves half a binding.
	const path = join(dir, `${keyOf(b)}.json`)
	writeFileSync(`${path}.tmp`, JSON.stringify(b))
	renameSync(`${path}.tmp`, path)
}

// bindings skips a file it cannot read, so the session's other rooms still
// come back.
function bindings(sessionId) {
	const dir = bindingsDir(sessionId)
	let files
	try {
		files = readdirSync(dir).filter((f) => f.endsWith(".json"))
	} catch {
		return []
	}
	return files.flatMap((f) => {
		try {
			const b = JSON.parse(readFileSync(join(dir, f), "utf8"))
			return typeof b?.repo === "string" && typeof b?.room === "string"
				? [b]
				: []
		} catch (err) {
			log(`binding ${f}: ${err.message}`)
			return []
		}
	})
}

/**
 * Starts the forward of a bound room and resolves true once it holds
 * main's delivery, or false when the room needs none, another reader holds
 * it or forward cannot start. Each batch is queued in the session and acked
 * in turn; a failed queue is nacked, so peer hands the batch over again.
 */
function startForward(session, b) {
	const key = keyOf(b)
	if (forwards.has(key)) return Promise.resolve(ready.has(key))
	const child = spawn(
		"peer",
		["forward", b.room, "--as", "main", "--copilot-session", session.sessionId],
		{
			cwd: b.repo,
			env: { ...process.env, PEER_REPO: b.repo },
			stdio: ["pipe", "pipe", "inherit"],
		},
	)
	forwards.set(key, child)
	return new Promise((resolve) => {
		let isGone = false
		let isEndAcked = false
		// answer writes an ack or nack while forward still reads them; a
		// late queue after forward gave up answers nobody.
		const answer = (msg, then) => {
			if (isGone || !child.stdin.writable) return
			child.stdin.write(`${JSON.stringify(msg)}\n`, (err) => {
				if (err) log(`room ${b.room}: ${err.message}`)
				else then?.()
			})
		}
		child.stdin.on("error", (err) => log(`room ${b.room}: ${err.message}`))
		// finish runs once, on a failed spawn or on exit; isClean is an exit
		// with status 0, after which forward has marked what it acked.
		const finish = (why, isClean) => {
			if (isGone) return
			isGone = true
			forwards.delete(key)
			const wasReady = ready.delete(key)
			resolve(false)
			// A forward that stops before it marked the acked end leaves main
			// to wait.
			if (wasReady && !(isClean && isEndAcked)) {
				log(`forward for room ${b.room} stopped: ${why}`)
				session
					.send({
						prompt: `The peer plugin stopped delivering peer room ${b.room}. Read it with peer wait as peer skills main says.`,
						mode: "enqueue",
						source: "system",
					})
					.catch((err) => log(String(err)))
			}
		}
		child.on("error", (err) => finish(err.message, false))
		child.on("exit", (code, signal) =>
			finish(`exit ${code ?? signal}`, code === 0),
		)
		createInterface({ input: child.stdout }).on("line", async (line) => {
			let ev
			try {
				ev = JSON.parse(line)
			} catch {
				return
			}
			if (ev.status === "ready") {
				ready.set(key, b)
				resolve(true)
			} else if (ev.status === "done") {
				resolve(false)
			} else if (ev.status === "batch") {
				try {
					await session.send({
						prompt: ev.text,
						mode: "enqueue",
						source: "system",
					})
				} catch (err) {
					answer({ nack: ev.batch, error: String(err) })
					return
				}
				answer({ ack: ev.batch }, () => {
					if (ev.batch === `${b.room}-end`) isEndAcked = true
				})
			} else if (ev.status === "retry") {
				log(`room ${b.room}: ${ev.error}`)
			}
		})
	})
}

const note = (rooms) =>
	`The peer plugin delivers the messages for main of peer room ${rooms.join(", ")} into this Copilot session: each batch comes as a turn of its own. Do not run peer wait for these rooms; peer refuses it while the plugin delivers.`

const bashCommand = (input) =>
	input.toolName === "bash" ? String(input.toolArgs?.command ?? "") : ""

// The canvas serves the chat of bound rooms on a loopback port. A random
// token in its URL and a Host check keep other pages and rebound names out.
const token = randomBytes(16).toString("hex")
let server

function serve() {
	if (server) return server
	server = new Promise((resolve, reject) => {
		// An error of one request answers it and never reaches the process,
		// so the canvas cannot stop delivery.
		const http = createServer((req, res) =>
			handle(req, res).catch((err) => {
				log(`canvas: ${err.message}`)
				if (!res.headersSent) res.writeHead(500)
				res.end()
			}),
		)
		const handle = async (req, res) => {
			const { port } = http.address()
			if (req.method !== "GET" || req.headers.host !== `127.0.0.1:${port}`) {
				res.writeHead(403).end()
				return
			}
			let url
			try {
				url = new URL(req.url, `http://127.0.0.1:${port}`)
			} catch {
				res.writeHead(400).end()
				return
			}
			res.setHeader("Cache-Control", "no-store")
			res.setHeader("X-Content-Type-Options", "nosniff")
			res.setHeader("Referrer-Policy", "no-referrer")
			res.setHeader(
				"Content-Security-Policy",
				"default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'",
			)
			if (url.searchParams.get("token") !== token) {
				res.writeHead(403).end()
				return
			}
			const file = {
				"/": ["canvas.html", "text/html"],
				"/canvas.js": ["canvas.js", "text/javascript"],
				"/canvas.css": ["canvas.css", "text/css"],
			}[url.pathname]
			if (file) {
				const body = readFileSync(join(here, file[0]), "utf8").replaceAll(
					"TOKEN",
					token,
				)
				res
					.writeHead(200, { "Content-Type": `${file[1]}; charset=utf-8` })
					.end(body)
				return
			}
			if (url.pathname === "/log") {
				// Only a room this session leads, never a path from the URL.
				const b =
					ready.get(url.searchParams.get("b")) ??
					openRooms.get(url.searchParams.get("b"))
				if (!b) {
					res.writeHead(404).end()
					return
				}
				try {
					const out = await peer(["log", b.room, "--json"], "", b.repo)
					const messages = out
						.split("\n")
						.filter(Boolean)
						.map((l) => JSON.parse(l))
					res
						.writeHead(200, { "Content-Type": "application/json" })
						.end(JSON.stringify({ room: b.room, messages }))
				} catch (err) {
					res
						.writeHead(500, { "Content-Type": "application/json" })
						.end(JSON.stringify({ error: String(err) }))
				}
				return
			}
			res.writeHead(404).end()
		}
		http.on("error", reject)
		http.listen(0, "127.0.0.1", () => resolve(http))
	})
	return server
}

// openRooms keeps the rooms a canvas shows, so it still shows a room after
// its forward delivered the end.
const openRooms = new Map()

const roomCanvas = createCanvas({
	id: "peer-room",
	displayName: "peer room",
	description: "Shows the chat of a peer room that this session leads as main.",
	inputSchema: {
		type: "object",
		properties: {
			room: {
				type: "string",
				description: "ID of the peer room; by default the one started last",
			},
		},
		additionalProperties: false,
	},
	open: async ({ input }) => {
		const rooms = [...ready.values(), ...openRooms.values()]
		const b = input?.room
			? rooms.findLast((r) => r.room === input.room)
			: [...ready.values()].at(-1)
		if (!b)
			throw new Error(
				"this session leads no peer room that the plugin delivers",
			)
		openRooms.set(keyOf(b), b)
		const http = await serve()
		const { port } = http.address()
		return {
			url: `http://127.0.0.1:${port}/?token=${token}&b=${keyOf(b)}&room=${encodeURIComponent(b.room)}`,
			title: `peer · ${b.room}`,
		}
	},
})

// pendingNote tells the model on its next prompt which rooms a resumed
// session gets delivered, without a turn of its own.
let pendingNote = ""

const session = await joinSession({
	canvases: [roomCanvas],
	// /peer TASK turns into a prompt for the plugin's skill, which holds the
	// instructions.
	commands: [
		{
			name: "peer",
			description:
				"Work on a task with other local agents in a peer room: /peer TASK",
			handler: ({ args }) =>
				session.send({
					prompt: args.trim()
						? `Use the peer skill for this task: ${args.trim()}`
						: "Use the peer skill.",
				}),
		},
	],
	hooks: {
		onUserPromptSubmitted: () => {
			if (!pendingNote) return
			const additionalContext = pendingNote
			pendingNote = ""
			return { additionalContext }
		},
		onPostToolUse: async (input) => {
			const command = bashCommand(input)
			if (!/\bpeer\s+start\b/.test(command)) return
			let room
			try {
				const out = await peer(
					["copilot-start"],
					JSON.stringify({
						tool_input: input.toolArgs,
						tool_response: input.toolResult?.textResultForLlm ?? "",
					}),
				)
				room = out.trim() && JSON.parse(out)
			} catch (err) {
				log(String(err))
				return
			}
			if (!room) return
			// Bind before starting forward, so a resume finds every forward.
			bind(session.sessionId, room)
			if (await startForward(session, { repo: room.repo, room: room.id }))
				return { additionalContext: note([room.id]) }
		},
		onPreToolUse: (input) => {
			const command = bashCommand(input)
			const room = [...ready.values()]
				.map((b) => b.room)
				.find((id) => new RegExp(`\\bpeer\\s+wait\\s+${id}\\b`).test(command))
			if (room) {
				return {
					permissionDecision: "deny",
					permissionDecisionReason: `The peer plugin delivers the messages for main of room ${room}; do not run peer wait for it.`,
				}
			}
		},
	},
})

// A resumed session gets its rooms back, ended ones too until their end is
// delivered; a new session after /clear has no bindings of its own.
const resumed = []
for (const b of bindings(session.sessionId)) {
	if (await startForward(session, b)) resumed.push(b.room)
}
if (resumed.length > 0) pendingNote = note(resumed)

process.on("SIGTERM", () => {
	for (const child of forwards.values()) child.stdin.end()
	server?.then((http) => http.close())
	process.exit(0)
})
