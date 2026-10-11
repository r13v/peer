import assert from "node:assert/strict"
import { test } from "node:test"
import { blockOrigin, fitFrame, placementOps, planPlacement } from "./layout.ts"
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

test("a slide frame keeps 16:9 when it grows", () => {
	const slide: FrameNode = {
		id: "f1",
		rev: 1,
		type: "frame",
		title: "S",
		order: 1,
		preset: "slide",
	}

	const size = fitFrame(slide, { w: 320, h: 180 }, { w: 300, h: 400 })

	assert.equal(Math.round((size.w / size.h) * 9), 16)
	assert.ok(size.h >= 400)
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
