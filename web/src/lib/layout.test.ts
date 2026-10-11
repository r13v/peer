import assert from "node:assert/strict"
import { test } from "node:test"
import {
	blockOrigin,
	placementOps,
	planPlacement,
	settleContainer,
	settleOps,
} from "./layout.ts"
import type { Board, BoardNode, FrameNode } from "./types.ts"

function board(nodes: BoardNode[]): Board {
	return {
		formatVersion: 1,
		id: "b_test",
		title: "Test",
		revision: 3,
		nodes,
		edges: [{ id: "e9", rev: 1, from: "n1", to: "n2" }],
		threads: [],
		deleted: [],
		nextId: 10,
	}
}

function overlaps(
	a: { x: number; y: number; w: number; h: number },
	b: typeof a,
): boolean {
	return (
		a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h
	)
}

test("a new block goes at the origin of an empty container and below placed nodes otherwise", () => {
	assert.deepEqual(blockOrigin([], { x: 24, y: 44 }), { x: 24, y: 44 })
	assert.deepEqual(
		blockOrigin(
			[
				{ x: 50, y: 0, w: 100, h: 80 },
				{ x: 10, y: 200, w: 100, h: 50 },
			],
			{ x: 0, y: 0 },
		),
		{ x: 10, y: 290 },
	)
})

test("unplaced nodes do not overlap placed ones and placed nodes keep their position", () => {
	const placed: BoardNode = {
		id: "n0",
		rev: 1,
		type: "card",
		text: "placed",
		x: 0,
		y: 0,
		w: 240,
		h: 80,
	}
	const b = board([
		placed,
		{ id: "n1", rev: 1, type: "card", text: "a" },
		{ id: "n2", rev: 1, type: "card", text: "b" },
	])

	const plan = planPlacement(b)

	assert.deepEqual(plan.unplaced, ["n1", "n2"])
	assert.equal(plan.positions.has("n0"), false)
	for (const id of plan.unplaced) {
		const p = plan.positions.get(id)
		assert.ok(p)
		assert.equal(
			overlaps({ ...p, w: 240, h: 80 }, { x: 0, y: 0, w: 240, h: 80 }),
			false,
		)
	}
	const n1 = plan.positions.get("n1")
	const n2 = plan.positions.get("n2")
	assert.ok(
		n1 && n2 && n1.y < n2.y,
		"an edge ranks its source above its target",
	)
})

test("an unplaced frame is sized around its unplaced children and placed itself", () => {
	const frame: FrameNode = {
		id: "f1",
		rev: 1,
		type: "frame",
		title: "F",
		order: 1,
	}
	const b = board([
		frame,
		{ id: "n1", rev: 1, type: "card", text: "a", parentId: "f1" },
	])

	const plan = planPlacement(b)
	const ops = placementOps(plan)

	assert.ok(plan.positions.has("f1"))
	assert.ok(plan.sizes.has("f1"))
	assert.deepEqual(
		ops.map((op) => op.op === "update" && op.id),
		["f1", "n1"],
	)
	const child = plan.positions.get("n1")
	assert.ok(child && child.y >= 44, "children start below the frame title")
})

test("measured sizes drive the layout and are written with the positions", () => {
	const b = board([
		{ id: "n1", rev: 1, type: "card", text: "40 lines" },
		{ id: "n2", rev: 1, type: "card", text: "b" },
	])
	const measured = new Map([
		["n1", { w: 240, h: 700 }],
		["n2", { w: 240, h: 40 }],
	])

	const plan = planPlacement(b, measured)
	const n1 = plan.positions.get("n1")
	const n2 = plan.positions.get("n2")

	assert.ok(
		n1 && n2 && n2.y >= n1.y + 700,
		"the target goes below the tall source",
	)
	assert.deepEqual(placementOps(plan), [
		{ op: "update", id: "n1", set: { ...n1, w: 240, h: 700 } },
		{ op: "update", id: "n2", set: { ...n2, w: 240, h: 40 } },
	])
})

test("a new node goes below a placed card that grew since its size was stored", () => {
	const b = board([
		{ id: "n0", rev: 1, type: "card", text: "long", x: 0, y: 0, w: 240, h: 80 },
		{ id: "n1", rev: 1, type: "card", text: "new" },
	])
	const measured = new Map([
		["n0", { w: 240, h: 700 }],
		["n1", { w: 240, h: 40 }],
	])

	const n1 = planPlacement(b, measured).positions.get("n1")

	assert.ok(n1 && n1.y >= 700, "the new card is not inside the grown one")
})

test("a card that grew pushes the column below it, its frame grows and pushes the next frame", () => {
	const nodes: BoardNode[] = [
		{
			id: "f1",
			type: "frame",
			rev: 1,
			title: "A",
			order: 1,
			x: 608,
			y: 0,
			w: 480,
			h: 673,
		},
		{
			id: "n2",
			type: "card",
			rev: 1,
			text: "",
			parentId: "f1",
			x: 24,
			y: 377,
			w: 240,
			h: 120,
		},
		{
			id: "n3",
			type: "card",
			rev: 1,
			text: "",
			parentId: "f1",
			x: 24,
			y: 553,
			w: 240,
			h: 194,
		},
		{
			id: "f4",
			type: "frame",
			rev: 1,
			title: "B",
			order: 2,
			x: 0,
			y: 713,
			w: 848,
			h: 575,
		},
		{
			id: "f5",
			type: "frame",
			rev: 1,
			title: "C",
			order: 3,
			x: 0,
			y: 177,
			w: 568,
			h: 320,
		},
	]
	// n2 rendered taller than stored: its bottom (611) now covers the top of n3 (553).
	const ops = settleOps(board(nodes), new Map([["n2", { w: 240, h: 234 }]]))
	const set = new Map(
		ops.map((op) => [
			op.op === "update" ? op.id : "",
			op.op === "update" ? op.set : {},
		]),
	)

	assert.deepEqual(set.get("n3"), { y: 377 + 234 + 24 })
	const frameH = (set.get("f1")?.h as number) ?? 0
	assert.ok(frameH >= 635 + 194, "the frame holds the pushed card")
	assert.ok(
		(set.get("f4")?.y as number) >= frameH,
		"the next frame starts below the grown one",
	)
	assert.equal(set.has("f5"), false, "a frame in another column stays")
	assert.equal(set.has("n2"), false)
})

test("settling leaves nodes on one row and nodes without overlap in place", () => {
	const moved = settleContainer([
		{ id: "a", x: 0, y: 0, w: 100, h: 100 },
		{ id: "b", x: 50, y: 0, w: 100, h: 100 }, // same row: overlap is the person's choice
		{ id: "c", x: 300, y: 50, w: 100, h: 100 }, // another column
		{ id: "d", x: 0, y: 110, w: 100, h: 50 }, // below with a small gap
	])

	assert.equal(moved.size, 0)
})

test("a push carries on down the column", () => {
	const rects = [
		{ id: "a", x: 0, y: 0, w: 100, h: 200 },
		{ id: "b", x: 0, y: 100, w: 100, h: 100 },
		{ id: "c", x: 0, y: 220, w: 100, h: 100 },
	]
	const moved = settleContainer(rects)
	const placed = rects.map((r) => ({ ...r, ...moved.get(r.id) }))

	for (const [i, upper] of placed.entries()) {
		for (const lower of placed.slice(i + 1)) {
			assert.equal(
				overlaps(upper, lower),
				false,
				`${upper.id} covers ${lower.id}`,
			)
		}
	}
})
