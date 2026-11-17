import { expect, mock, type TestBody, test } from "claude-code/testing"

// These tests pin how the plugin takes a room without a tool.call hook,
// which would turn Code Mode off: peer records the session that starts or
// joins a room, and the plugin holds the delivery of the roles it finds
// for this session in peer watch.

const SID = "session-1"

const room = (id: string, session: string) => ({
	id,
	repo: "/repo",
	started_at: "2026-10-05T20:00:00Z",
	members: [{ role: "main", agent: "claude", session }],
})

// main starts the plugin with a watch that prints the rooms after ready,
// as when they start while the session runs; it returns the spawns.
async function main(
	$: Parameters<TestBody>[0],
	on: Parameters<TestBody>[1],
	rooms: unknown[],
) {
	const spawned: string[][] = []
	on("process.spawn", async function* (_, e) {
		spawned.push([...e.argv])
		if (e.argv[1] !== "watch")
			return { value: { code: 0, signal: null } } as never
		yield { stream: "stdout", text: '{"event":"ready"}\n' }
		yield {
			stream: "stdout",
			text: rooms
				.map((r) => `${JSON.stringify({ event: "room", room: r })}\n`)
				.join(""),
		}
		await new Promise(() => {})
		return { code: 0, signal: null } as never
	})
	on(
		"process.run",
		() => ({ value: { exitCode: 0, stdout: "", stderr: "" } }) as never,
	)
	on("session.start", (_, e) => ({ cwd: e.cwd }) as never)
	on("session.cwd", () => ({ value: "/repo" }) as never)
	on("session.id", () => ({ value: SID }) as never)
	on("ui.open", () => ({ value: { isPlaced: true } }) as never)
	on("ui.scroll", () => ({ value: {} }) as never)
	on("ui.panes", () => ({ value: [] }) as never)
	await $.session.start({ source: "startup", cwd: "/repo" } as never)
	await new Promise((resolve) => setTimeout(resolve, 100))
	return spawned
}

test("a room this session starts is held for main; another session's is not", async ($, on) => {
	const spawned = await main($, on, [
		room("csv-export", SID),
		room("other", "session-2"),
	])
	expect(spawned.filter((argv) => argv[1] === "hold")).toEqual([
		["peer", "hold", "csv-export", "--as", "main"],
	])
})

test("a room that starts while watch is down is held once watch is back", async ($, on) => {
	const clock = mock.clock(on)
	const spawned: string[][] = []
	let watches = 0
	on("process.spawn", async function* (_, e) {
		spawned.push([...e.argv])
		if (e.argv[1] !== "watch")
			return { value: { code: 0, signal: null } } as never
		watches += 1
		if (watches === 1) {
			yield { stream: "stdout", text: '{"event":"ready"}\n' }
			yield { stream: "stderr", text: "peer: watch broke\n" }
			return { value: { code: 1, signal: null } } as never
		}
		yield {
			stream: "stdout",
			// Back, watch prints the rooms, then ready, as it does at its start.
			text: `${JSON.stringify({ event: "room", room: room("csv-export", SID) })}\n{"event":"ready"}\n`,
		}
		await new Promise(() => {})
		return { value: { code: 0, signal: null } } as never
	})
	on(
		"process.run",
		() => ({ value: { exitCode: 0, stdout: "", stderr: "" } }) as never,
	)
	on("ui.log", () => ({ value: undefined }) as never)
	on("session.start", (_, e) => ({ cwd: e.cwd }) as never)
	on("session.cwd", () => ({ value: "/repo" }) as never)
	on("session.id", () => ({ value: SID }) as never)
	on("ui.open", () => ({ value: { isPlaced: true } }) as never)
	on("ui.scroll", () => ({ value: {} }) as never)
	on("ui.panes", () => ({ value: [] }) as never)
	await $.session.start({ source: "startup", cwd: "/repo" } as never)
	await new Promise((resolve) => setTimeout(resolve, 50))
	await clock.advance(10_000)
	await new Promise((resolve) => setTimeout(resolve, 100))
	expect(watches).toBe(2)
	expect(spawned.filter((argv) => argv[1] === "hold")).toEqual([
		["peer", "hold", "csv-export", "--as", "main"],
	])
})
