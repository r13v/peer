import { describeError } from "../lib/api.ts"
import { dirName } from "../lib/catalog.ts"
import type { BoardRef } from "../lib/types.ts"

export function BoardRefItem({ board }: { board: BoardRef }) {
	const name = board.title || dirName(board.path)

	return (
		<li className="board-ref">
			{board.error ? (
				<span className="error-name">{name}</span>
			) : (
				<a href={board.url}>{name}</a>
			)}
			<span className="muted path" title={board.path}>
				{board.path}
			</span>
			{board.error ? (
				<span className="error">{describeError(board.error)}</span>
			) : (
				<span className="muted">
					{board.revision !== undefined && `rev ${board.revision}`}
					{board.mtime && ` · ${new Date(board.mtime).toLocaleString()}`}
				</span>
			)}
		</li>
	)
}
