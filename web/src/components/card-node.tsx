import { type NodeProps, NodeResizer } from "@xyflow/react"
import { useBoardActions } from "../lib/board-context.ts"
import type { FlowNode } from "../lib/flow.ts"
import { InlineText } from "./inline-text.tsx"
import { NodeHandles } from "./node-handles.tsx"
import { ThreadBadge } from "./thread-badge.tsx"

export function CardNodeView({ id, data, selected }: NodeProps<FlowNode>) {
	const actions = useBoardActions()
	const { node } = data
	if (node.type !== "card") {
		return null
	}

	return (
		<div
			className={`card color-${node.color ?? "none"}`}
			style={{ minHeight: node.h }}
		>
			<NodeResizer
				isVisible={selected}
				minWidth={80}
				minHeight={32}
				onResizeStart={actions.startResize}
				onResizeEnd={(_, params) => actions.endResize(id, params)}
			/>
			<InlineText
				id={id}
				className="card-text"
				value={node.text}
				placeholder="Double click to edit"
				multiline
				onCommit={(text) => actions.updateNode(id, { text })}
			/>
			<ThreadBadge id={id} />
			<NodeHandles />
		</div>
	)
}
