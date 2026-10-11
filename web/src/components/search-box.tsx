import { useState } from "react"
import { nodeLabel, nodeText } from "../lib/board.ts"
import type { Board } from "../lib/types.ts"
import { Icon } from "./icon.tsx"

const resultLimit = 20

export function SearchBox({
	board,
	onPick,
}: {
	board: Board
	onPick: (id: string) => void
}) {
	const [query, setQuery] = useState("")
	const needle = query.trim().toLowerCase()
	const results = needle
		? board.nodes
				.filter((n) => nodeText(n).toLowerCase().includes(needle))
				.slice(0, resultLimit)
		: []

	function pick(id: string) {
		setQuery("")
		onPick(id)
	}

	return (
		<div className="search">
			<label className="field search-field">
				<Icon name="search" />
				<input
					type="search"
					placeholder="Search board"
					aria-label="Search board"
					value={query}
					onChange={(e) => setQuery(e.target.value)}
					onKeyDown={(e) => {
						if (e.key === "Enter" && results[0]) {
							pick(results[0].id)
						} else if (e.key === "Escape") {
							setQuery("")
						}
					}}
				/>
			</label>
			{needle && (
				<ul className="search-results">
					{results.length === 0 && <li className="empty muted">No matches</li>}
					{results.map((n) => (
						<li key={n.id}>
							<button type="button" onClick={() => pick(n.id)}>
								<span className="kind">{n.type}</span> {nodeLabel(n)}
							</button>
						</li>
					))}
				</ul>
			)}
		</div>
	)
}
