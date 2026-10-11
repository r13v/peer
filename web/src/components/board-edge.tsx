import {
	BaseEdge,
	EdgeLabelRenderer,
	type EdgeProps,
	getBezierPath,
} from "@xyflow/react"
import type { FlowEdge } from "../lib/flow.ts"
import { ThreadBadge } from "./thread-badge.tsx"

export function BoardEdgeView(props: EdgeProps<FlowEdge>) {
	const { id, data, markerEnd } = props
	const [path, labelX, labelY] = getBezierPath(props)
	const label = data?.edge.label

	return (
		<>
			<BaseEdge id={id} path={path} markerEnd={markerEnd} />
			<EdgeLabelRenderer>
				<div
					className="edge-label nopan"
					style={{
						transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
					}}
				>
					{label}
					<ThreadBadge id={id} />
				</div>
			</EdgeLabelRenderer>
		</>
	)
}
