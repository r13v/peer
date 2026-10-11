import { useBoardActions } from "../lib/board-context.ts"

export function ThreadBadge({ id }: { id: string }) {
	const count = useBoardActions().threadCounts.get(id) ?? 0
	if (count === 0) {
		return null
	}

	return (
		<span className="badge" title={`${count} open thread(s)`}>
			{count}
		</span>
	)
}
