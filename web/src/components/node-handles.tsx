import { Handle, type Position } from "@xyflow/react"
import { sides } from "../lib/flow.ts"

// A handle on every side. The canvas connects in loose mode, so any handle starts or ends an
// edge; a drawn edge attaches to the sides that face each other (`withSides`).
export function NodeHandles() {
	return (
		<>
			{sides.map((side) => (
				<Handle
					key={side}
					id={side}
					type="source"
					position={side as Position}
				/>
			))}
		</>
	)
}
