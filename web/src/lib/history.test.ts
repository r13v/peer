import assert from "node:assert/strict"
import { test } from "node:test"
import {
	expectFor,
	historyInvalidated,
	invertPack,
	needsReload,
	rewriteExpect,
	type Step,
} from "./history.ts"
import type { ApplyResult, Board, BoardNode, Thread } from "./types.ts"

function board(
	revision: number,
	nodes: BoardNode[],
	extra: Partial<Board> = {},
): Board {
	return {
		formatVersion: 1,
		id: "b_test",
		title: "Test",
		revision,
		nodes,
		edges: [],
		threads: [],
		deleted: [],
		nextId: 100,
		...extra,
	}
}

const frame: BoardNode = {
	id: "f1",
	rev: 1,
	type: "frame",
	title: "F",
	order: 1,
	x: 100,
	y: 100,
	w: 400,
	h: 300,
}
const child: BoardNode = {
	id: "n2",
	rev: 1,
	type: "card",
	text: "child",
	parentId: "f1",
	x: 10,
	y: 50,
}
const inner: BoardNode = {
	id: "n3",
	rev: 1,
	type: "card",
	text: "inner",
	parentId: "f1",
}

test("undo of an add deletes the created node and its created edge, edge first", () => {
	const before = board(1, [])
	const added: BoardNode = {
		id: "n5",
		rev: 2,
		type: "card",
		text: "new",
		x: 0,
		y: 0,
	}
	const other: BoardNode = { id: "n6", rev: 2, type: "card", text: "other" }
	const after = board(2, [added, other], {
		edges: [{ id: "e7", rev: 2, from: "n5", to: "n6" }],
	})

	const inverse = invertPack(before, after, ["n5", "n6", "e7"])

	assert.deepEqual(inverse, [
		{ op: "delete", id: "e7" },
		{ op: "delete", id: "n5" },
		{ op: "delete", id: "n6" },
	])
})

test("undo of an update sets the previous values and removes fields that were absent", () => {
	const before = board(1, [
		{ id: "n1", rev: 1, type: "card", text: "old", x: 0, y: 0 },
	])
	const after = board(2, [
		{ id: "n1", rev: 2, type: "card", text: "new", color: "red", x: 0, y: 0 },
	])

	const inverse = invertPack(before, after, ["n1"])

	assert.deepEqual(inverse, [
		{ op: "update", id: "n1", set: { text: "old", color: null } },
	])
})

test("undo of a frame ungroup restores the frame before moving its children back", () => {
	const before = board(1, [frame, child])
	const moved: BoardNode = {
		id: "n2",
		rev: 2,
		type: "card",
		text: "child",
		x: 110,
		y: 150,
	}
	const after = board(2, [moved], { deleted: [{ id: "f1", rev: 2 }] })

	const inverse = invertPack(before, after, ["f1", "n2"])

	assert.deepEqual(inverse, [
		{ op: "restore", node: frame },
		{ op: "update", id: "n2", set: { parentId: "f1", x: 10, y: 50 } },
	])
})

test("undo of a cascade delete restores the subtree parents first, then edges; an orphaned thread needs no op", () => {
	const edge = { id: "e4", rev: 1, from: "n2", to: "n3" }
	const thread: Thread = {
		id: "t5",
		rev: 1,
		target: { kind: "node", id: "n2" },
		resolved: false,
		orphaned: false,
		messages: [],
	}
	const before = board(1, [child, inner, frame], {
		edges: [edge],
		threads: [thread],
	})
	const orphaned = { ...thread, rev: 2, orphaned: true, targetLabel: "child" }
	const after = board(2, [], { threads: [orphaned] })

	const inverse = invertPack(before, after, ["n2", "n3", "f1", "e4", "t5"])

	assert.deepEqual(inverse, [
		{ op: "restore", node: frame },
		{ op: "restore", node: child },
		{ op: "restore", node: inner },
		{ op: "restore", edge },
	])
})

test("undo of a comment deletes the thread; undo of resolve resolves the opposite way", () => {
	const open: Thread = {
		id: "t1",
		rev: 1,
		target: { kind: "board" },
		resolved: false,
		orphaned: false,
		messages: [],
	}
	const before = board(1, [], { threads: [open] })
	const created: Thread = { ...open, id: "t2", rev: 2 }
	const after = board(2, [], {
		threads: [{ ...open, rev: 2, resolved: true }, created],
	})

	const inverse = invertPack(before, after, ["t1", "t2"])

	assert.deepEqual(inverse, [
		{ op: "resolve", thread: "t1", resolved: false },
		{ op: "delete", id: "t2" },
	])
})

test("a reply has no inverse", () => {
	const message = {
		id: "m3",
		author: { role: "user" },
		body: "ok",
		createdAt: "2026-01-01T00:00:00Z",
	}
	const thread: Thread = {
		id: "t1",
		rev: 1,
		target: { kind: "board" },
		resolved: false,
		orphaned: false,
		messages: [],
	}
	const before = board(1, [], { threads: [thread] })
	const after = board(2, [], {
		threads: [{ ...thread, rev: 2, messages: [message] }],
	})

	assert.deepEqual(invertPack(before, after, ["t1"]), [])
})

test("expect holds every created, changed and deleted id at the new revision", () => {
	const result: ApplyResult = {
		revision: 9,
		refs: {},
		created: ["n5"],
		changed: ["n1", "t2"],
		deleted: ["e3"],
		warnings: [],
	}

	assert.deepEqual(expectFor(result), { n5: 9, n1: 9, t2: 9, e3: 9 })
})

test("an own undo moves the expectations of the touched entities only", () => {
	const steps: Step[] = [
		{ ops: [], expect: { n1: 3, n2: 3 } },
		{ ops: [], expect: { n1: 5 } },
	]

	const rewritten = rewriteExpect(steps, ["n1"], 8)

	assert.deepEqual(
		rewritten.map((s) => s.expect),
		[{ n1: 8, n2: 3 }, { n1: 8 }],
	)
	assert.deepEqual(steps[0].expect, { n1: 3, n2: 3 })
})

test("an own service pack keeps an expectation that another writer made stale", () => {
	const steps: Step[] = [{ ops: [], expect: { n1: 3, n2: 4 } }]

	const rewritten = rewriteExpect(steps, ["n1", "n2"], 7, { n1: 3, n2: 6 })

	assert.deepEqual(rewritten[0].expect, { n1: 7, n2: 4 })
})

test("an event reloads unless its hash is the current snapshot's, even a hash seen before", () => {
	const current = { board: board(5, []), hash: "h5" }

	assert.equal(
		needsReload(current, { revision: 5, hash: "h5", external: false }),
		false,
	)
	assert.equal(
		needsReload(current, { revision: 4, hash: "h4", external: false }),
		true,
	)
})

test("a reloaded snapshot with another hash and no newer revision invalidates the history", () => {
	const current = { board: board(5, []), hash: "h5" }

	assert.equal(
		historyInvalidated(current, { board: board(4, []), hash: "h4" }, 5),
		true,
	)
	assert.equal(
		historyInvalidated(current, { board: board(5, []), hash: "x5" }, 5),
		true,
	)
	assert.equal(
		historyInvalidated(current, { board: board(6, []), hash: "h6" }, 5),
		false,
	)
	assert.equal(historyInvalidated(current, current, 5), false)
	assert.equal(historyInvalidated(undefined, current, -1), false)
})
