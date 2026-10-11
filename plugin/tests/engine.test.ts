import { expect, type TestBody, test } from "claude-code/testing"

// These tests pin the hooks the plugin relies on to keep one reader as main:
// the engine must route Bash calls through tool.call and take the plugin's
// answer or deny in place of running the command.

const ROOM = {
	id: "csv-export",
	repo: "/repo",
	started_at: "2026-10-05T20:00:00Z",
	members: [{ role: "main", agent: "claude" }],
}

// main starts the plugin with stubs below it and makes this session main of
// the room through a peer start; it returns the commands that reached Bash.
async function main($: Parameters<TestBody>[0], on: Parameters<TestBody>[1]) {
	const bash: string[] = []
	on("process.spawn", async function* (_, e, next) {
		if (e.argv[1] !== "watch") return yield* next(e)
		yield {
			stream: "stdout",
			text: `${JSON.stringify({ event: "room", room: ROOM })}\n{"event":"ready"}\n`,
		}
		await new Promise(() => {})
		return { code: 0, signal: null } as never
	})
	on(
		"process.run",
		() => ({ value: { exitCode: 0, stdout: "", stderr: "" } }) as never,
	)
	on("prompt.submit", (_, e) => ({ text: e.text }) as never)
	on("tool.call", { tool: "Bash" }, (_, e) => {
		bash.push((e as { command: string }).command)
		return {
			result: {
				stdout: `${JSON.stringify(ROOM)}\n`,
				stderr: "",
				interrupted: false,
			},
		} as never
	})
	on("session.start", (_, e) => ({ cwd: e.cwd }) as never)
	on("session.cwd", () => ({ value: "/repo" }) as never)
	on("ui.open", () => ({ value: { isPlaced: true } }) as never)
	on("ui.scroll", () => ({ value: {} }) as never)
	on("ui.panes", () => ({ value: [] }) as never)
	await $.session.start({ source: "startup", cwd: "/repo" } as never)
	await new Promise((resolve) => setTimeout(resolve, 50))
	const started = await $.tool.call({
		tool: "Bash",
		command: "peer start csv-export --agent claude",
	} as never)
	expect(JSON.stringify(started)).toContain("Do not run peer wait")
	await new Promise((resolve) => setTimeout(resolve, 50))
	return bash
}

test("main's peer wait is answered by the plugin and never runs", async ($, on) => {
	const bash = await main($, on)
	const answer = await $.tool.call({
		tool: "Bash",
		command: "peer wait csv-export --as main",
	} as never)
	expect(JSON.stringify(answer)).toContain('\\"status\\":\\"timeout\\"')
	expect(bash).toEqual(["peer start csv-export --agent claude"])
})

test("a line with main's peer wait and another command is denied", async ($, on) => {
	const bash = await main($, on)
	const answer = await $.tool.call({
		tool: "Bash",
		command: "peer wait csv-export --as main && git status",
	} as never)
	expect(JSON.stringify(answer)).toContain("deny")
	expect(bash).toEqual(["peer start csv-export --agent claude"])
})

test("a peer wait for a room the plugin does not know runs as usual", async ($, on) => {
	const bash = await main($, on)
	await $.tool.call({
		tool: "Bash",
		command: "peer wait other-room --as main",
	} as never)
	expect(bash).toContain("peer wait other-room --as main")
})
