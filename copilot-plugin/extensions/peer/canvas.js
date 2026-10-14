// Shows a peer room's chat, newest first, and reloads it every 2 seconds.
// Message text goes in as text, never as HTML.
const params = new URLSearchParams(location.search)
const room = params.get("room")
const token = params.get("token")
const binding = params.get("b")
const colors = [
	"#0969da",
	"#1a7f37",
	"#9a6700",
	"#cf222e",
	"#8250df",
	"#bf3989",
	"#0a7ea4",
]
const color = (who) =>
	colors[
		[...who].reduce((h, c) => (h * 31 + c.charCodeAt(0)) >>> 0, 0) %
			colors.length
	]
document.getElementById("room").textContent = room
let shown = ""

async function load() {
	const state = document.getElementById("state")
	try {
		const res = await fetch(
			`log?token=${encodeURIComponent(token)}&b=${encodeURIComponent(binding)}`,
		)
		if (!res.ok) throw new Error(`HTTP ${res.status}`)
		const { messages } = await res.json()
		state.textContent = `${messages.length} messages`
		const key = messages.at(-1)?.id ?? ""
		if (key === shown) return
		shown = key
		const chat = document.getElementById("chat")
		chat.replaceChildren(
			...messages.reverse().map((m) => {
				const card = document.createElement("div")
				card.className = m.from === "peer" ? "msg peer" : "msg"
				card.style.setProperty("--who", color(m.from))
				const meta = document.createElement("div")
				meta.className = "meta"
				meta.textContent = `${new Date(m.at).toLocaleTimeString()} · ${m.from} → ${m.to === "*" ? "all" : m.to}`
				const text = document.createElement("div")
				text.className = "text"
				text.textContent = m.text
				card.append(meta, text)
				return card
			}),
		)
	} catch (err) {
		state.textContent = String(err)
	}
}

void load()
setInterval(load, 2000)
