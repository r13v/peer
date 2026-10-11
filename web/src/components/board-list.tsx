import type { BoardRef } from "../lib/types.ts"
import { BoardRefItem } from "./board-ref-item.tsx"

export function BoardList({ boards }: { boards: BoardRef[] }) {
	if (boards.length === 0) {
		return <p className="muted empty">No boards</p>
	}

	return (
		<ul className="board-list">
			{boards.map((board) => (
				<BoardRefItem key={board.key} board={board} />
			))}
		</ul>
	)
}
