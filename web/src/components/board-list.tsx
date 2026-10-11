import type { BoardRef } from "../lib/types.ts"
import { BoardRefItem } from "./board-ref-item.tsx"

export function BoardList({ boards }: { boards: BoardRef[] }) {
	return (
		<ul className="board-list">
			{boards.map((board) => (
				<BoardRefItem key={board.key} board={board} />
			))}
		</ul>
	)
}
