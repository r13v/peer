import { useBoardActions } from "../lib/board-context.ts"
import { Icon } from "./icon.tsx"

// Shows that a node or an edge has threads; a click opens them in the thread panel.
export function ThreadBadge({ id }: { id: string }) {
	const { threadCounts, showThreads } = useBoardActions()
	const count = threadCounts.get(id)
	if (!count) {
		return null
	}
	const title =
		count.open > 0
			? `${count.open} open thread(s); click to show`
			: `${count.resolved} resolved thread(s); click to show`

	return (
		<button
			type="button"
			className={
				count.open > 0 ? "badge nodrag nopan" : "badge quiet nodrag nopan"
			}
			title={title}
			aria-label={title}
			onClick={(e) => {
				e.stopPropagation()
				showThreads(id)
			}}
			onDoubleClick={(e) => e.stopPropagation()}
		>
			<Icon name="message" size={11} />
			{count.open > 0 && count.open}
		</button>
	)
}
