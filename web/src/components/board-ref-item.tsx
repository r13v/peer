import { describeError } from "../lib/api.ts"
import { dirName } from "../lib/catalog.ts"
import { fullTime, timeAgo } from "../lib/time.ts"
import type { BoardRef } from "../lib/types.ts"
import { Icon } from "./icon.tsx"

export function BoardRefItem({ board }: { board: BoardRef }) {
	const name = board.title || dirName(board.path)
	const body = (
		<>
			<span className="board-ref-icon">
				<Icon name="board" />
			</span>
			<span className="board-ref-main">
				<span
					className={board.error ? "board-ref-name error" : "board-ref-name"}
				>
					{name}
				</span>
				{/* Right-to-left clipping keeps the end of a long path visible. */}
				<span className="path" dir="rtl" title={board.path}>
					<bdi>{board.path}</bdi>
				</span>
			</span>
			{board.error ? (
				<span className="error board-ref-meta">
					{describeError(board.error)}
				</span>
			) : (
				<span className="board-ref-meta">
					{board.mtime && (
						<time dateTime={board.mtime} title={fullTime(board.mtime)}>
							{timeAgo(board.mtime)}
						</time>
					)}
					{board.revision !== undefined && (
						<span className="chip">rev {board.revision}</span>
					)}
				</span>
			)}
		</>
	)

	return (
		<li>
			{board.error ? (
				<div className="board-ref broken">{body}</div>
			) : (
				<a className="board-ref" href={board.url}>
					{body}
				</a>
			)}
		</li>
	)
}
