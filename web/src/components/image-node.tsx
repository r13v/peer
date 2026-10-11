import { type NodeProps, NodeResizer } from "@xyflow/react"
import { assetUrl } from "../lib/api.ts"
import { useBoardActions } from "../lib/board-context.ts"
import type { FlowNode } from "../lib/flow.ts"
import { InlineText } from "./inline-text.tsx"
import { NodeHandles } from "./node-handles.tsx"
import { ThreadBadge } from "./thread-badge.tsx"

export function ImageNodeView({ id, data, selected }: NodeProps<FlowNode>) {
	const actions = useBoardActions()
	const { node } = data
	if (node.type !== "image") {
		return null
	}

	return (
		<div className="image-node">
			<NodeResizer
				isVisible={selected}
				minWidth={60}
				minHeight={60}
				onResizeStart={actions.startResize}
				onResizeEnd={(_, params) => actions.endResize(id, params)}
			/>
			<img
				src={assetUrl(actions.boardKey, node.asset)}
				alt={node.caption ?? ""}
				draggable={false}
			/>
			<InlineText
				id={id}
				className="caption"
				value={node.caption ?? ""}
				placeholder="Add caption"
				onCommit={(caption) =>
					actions.updateNode(id, { caption: caption || null })
				}
			/>
			<ThreadBadge id={id} />
			<NodeHandles />
		</div>
	)
}
