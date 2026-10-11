import { Handle, type NodeProps, NodeResizer, Position } from "@xyflow/react"
import { useBoardActions } from "../lib/board-context.ts"
import type { FlowNode } from "../lib/flow.ts"
import { InlineText } from "./inline-text.tsx"
import { ThreadBadge } from "./thread-badge.tsx"

export function FrameNodeView({ id, data, selected }: NodeProps<FlowNode>) {
	const actions = useBoardActions()
	const { node } = data
	if (node.type !== "frame") {
		return null
	}

	return (
		<div className={node.preset === "slide" ? "frame slide" : "frame"}>
			<NodeResizer
				isVisible={selected}
				minWidth={160}
				minHeight={90}
				keepAspectRatio={node.preset === "slide"}
				onResizeStart={actions.startResize}
				onResizeEnd={(_, params) => actions.endResize(id, params)}
			/>
			<Handle type="target" position={Position.Top} />
			<div className="frame-title">
				<span className="frame-order">{node.order}</span>
				<InlineText
					id={id}
					className="frame-name"
					value={node.title}
					placeholder="Untitled frame"
					onCommit={(title) => actions.updateNode(id, { title })}
				/>
				<ThreadBadge id={id} />
			</div>
			<Handle type="source" position={Position.Bottom} />
		</div>
	)
}
