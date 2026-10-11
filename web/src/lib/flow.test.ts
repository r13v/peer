import assert from "node:assert/strict"
import { test } from "node:test"
import { type FlowEdge, type FlowNode, facingSides, withSides } from "./flow.ts"

const box = (x: number, y: number) => ({ x, y, w: 240, h: 100 })

test("an edge joins the sides that face each other", () => {
	assert.deepEqual(facingSides(box(0, 0), box(400, 20)), ["right", "left"])
	assert.deepEqual(facingSides(box(400, 20), box(0, 0)), ["left", "right"])
	assert.deepEqual(facingSides(box(0, 0), box(30, 200)), ["bottom", "top"])
	assert.deepEqual(facingSides(box(30, 200), box(0, 0)), ["top", "bottom"])
})

test("a node in a frame is placed by its board position, not its position in the frame", () => {
	const node = (
		id: string,
		x: number,
		y: number,
		parentId?: string,
	): FlowNode => ({
		id,
		type: "card",
		position: { x, y },
		parentId,
		width: 240,
		height: 100,
		data: { node: { id, rev: 1, type: "card", text: "" } },
	})
	// n2 sits at (24, 44) in a frame at (600, 0): on the board it is right of n1, not below it.
	const nodes = [node("n1", 0, 0), node("f1", 600, 0), node("n2", 24, 44, "f1")]
	const edge: FlowEdge = { id: "e1", source: "n1", target: "n2" }

	const [shown] = withSides([edge], nodes)

	assert.equal(shown.sourceHandle, "right")
	assert.equal(shown.targetHandle, "left")
})
