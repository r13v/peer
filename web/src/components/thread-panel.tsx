import { type FormEvent, useState } from "react"
import { nodeLabel, nodeMap } from "../lib/board.ts"
import type { Board, Target, Thread } from "../lib/types.ts"
import { ThreadView } from "./thread-view.tsx"

type Props = {
	board: Board
	selection: Target
	onComment: (target: Target, body: string) => void
	onReply: (thread: string, body: string) => void
	onResolve: (thread: string, resolved: boolean) => void
	onFocus: (id: string) => void
}

function targetLabel(board: Board, target: Target): string {
	if (target.kind === "board") {
		return "Board"
	}
	const nodes = nodeMap(board)
	const node = nodes.get(target.id)
	if (node) {
		return nodeLabel(node)
	}
	const edge = board.edges.find((e) => e.id === target.id)
	if (!edge) {
		return target.id
	}
	const from = nodes.get(edge.from)
	const to = nodes.get(edge.to)
	const ends = `${from ? nodeLabel(from) : edge.from} → ${to ? nodeLabel(to) : edge.to}`

	return edge.label ? `${edge.label} (${ends})` : ends
}

function threadLabel(board: Board, thread: Thread): string {
	if (thread.orphaned) {
		return `${thread.targetLabel ?? "?"} (deleted)`
	}

	return targetLabel(board, thread.target)
}

export function ThreadPanel({
	board,
	selection,
	onComment,
	onReply,
	onResolve,
	onFocus,
}: Props) {
	const [body, setBody] = useState("")
	const [showResolved, setShowResolved] = useState(false)
	const open = board.threads.filter((t) => !t.resolved)
	const resolved = board.threads.filter((t) => t.resolved)
	const shown = showResolved ? [...open, ...resolved] : open

	function submit(e: FormEvent) {
		e.preventDefault()
		const text = body.trim()
		if (!text) {
			return
		}
		onComment(selection, text)
		setBody("")
	}

	function focusFor(thread: Thread): (() => void) | undefined {
		const { target } = thread
		if (thread.orphaned || target.kind === "board") {
			return undefined
		}

		return () => onFocus(target.id)
	}

	return (
		<aside className="thread-panel">
			<form className="new-thread" onSubmit={submit}>
				<label htmlFor="new-thread">
					New thread on: {targetLabel(board, selection)}
				</label>
				<textarea
					id="new-thread"
					value={body}
					rows={3}
					onChange={(e) => setBody(e.target.value)}
				/>
				<button type="submit" disabled={!body.trim()}>
					Comment
				</button>
			</form>
			<div className="thread-filter">
				<span>
					{open.length} open, {resolved.length} resolved
				</span>
				<label>
					<input
						type="checkbox"
						checked={showResolved}
						onChange={(e) => setShowResolved(e.target.checked)}
					/>{" "}
					Show resolved
				</label>
			</div>
			{shown.map((thread) => (
				<ThreadView
					key={thread.id}
					thread={thread}
					label={threadLabel(board, thread)}
					onFocus={focusFor(thread)}
					onReply={(text) => onReply(thread.id, text)}
					onResolve={(value) => onResolve(thread.id, value)}
				/>
			))}
		</aside>
	)
}
