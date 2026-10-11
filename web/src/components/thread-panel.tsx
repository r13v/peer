import { type FormEvent, useEffect, useRef, useState } from "react"
import { nodeLabel, nodeMap } from "../lib/board.ts"
import type { Board, Target, Thread } from "../lib/types.ts"
import { Icon } from "./icon.tsx"
import { submitOnEnter, ThreadView } from "./thread-view.tsx"

type Props = {
	board: Board
	selection: Target
	onComment: (target: Target, body: string) => void
	onReply: (thread: string, body: string) => void
	onResolve: (thread: string, resolved: boolean) => void
	onFocus: (id: string) => void
	focus?: { id: string } // threads of a node or edge to show, from a click on its badge
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

// The kind of the target as a person sees it: a node is a card or an image.
function targetKind(board: Board, target: Target): string {
	if (target.kind === "node") {
		return board.nodes.find((n) => n.id === target.id)?.type ?? "node"
	}

	return target.kind
}

export function ThreadPanel({
	board,
	selection,
	onComment,
	onReply,
	onResolve,
	onFocus,
	focus,
}: Props) {
	const [body, setBody] = useState("")
	const [showResolved, setShowResolved] = useState(false)
	const open = board.threads.filter((t) => !t.resolved)
	const resolved = board.threads.filter((t) => t.resolved)
	const shown = showResolved ? resolved : open
	const [highlight, setHighlight] = useState<{ id: string }>()
	const list = useRef<HTMLDivElement>(null)
	const handled = useRef<{ id: string }>(undefined)

	// The tab with the target's threads: open ones when it has any, resolved ones otherwise.
	useEffect(() => {
		// Board updates rerun the effect; only a new click switches the tab.
		if (!focus || handled.current === focus) {
			return
		}
		handled.current = focus
		const hasOpen = board.threads.some(
			(t) =>
				!t.resolved && t.target.kind !== "board" && t.target.id === focus.id,
		)
		setShowResolved(!hasOpen)
		setHighlight(focus)
	}, [focus, board.threads])

	// Runs after the tab renders, so the target's threads are in the list.
	useEffect(() => {
		if (!highlight) {
			return
		}
		const first = list.current?.querySelector(
			`[data-target="${CSS.escape(highlight.id)}"]`,
		)
		first?.scrollIntoView({ block: "start", behavior: "smooth" })
		const timer = setTimeout(() => setHighlight(undefined), 1800)

		return () => clearTimeout(timer)
	}, [highlight])

	function submit(e: FormEvent) {
		e.preventDefault()
		const text = body.trim()
		if (!text) {
			return
		}
		onComment(selection, text)
		setBody("")
		setShowResolved(false)
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
			<header className="panel-head">
				<h2>Threads</h2>
				<div className="segmented" role="tablist">
					<button
						type="button"
						role="tab"
						aria-selected={!showResolved}
						onClick={() => setShowResolved(false)}
					>
						Open <span className="count">{open.length}</span>
					</button>
					<button
						type="button"
						role="tab"
						aria-selected={showResolved}
						onClick={() => setShowResolved(true)}
					>
						Resolved <span className="count">{resolved.length}</span>
					</button>
				</div>
			</header>
			<div className="thread-list" ref={list}>
				{shown.length === 0 && (
					<p className="empty-state muted">
						{showResolved
							? "No resolved threads."
							: "No open threads. Select a node or an edge and leave a comment below."}
					</p>
				)}
				{shown.map((thread) => (
					<ThreadView
						key={thread.id}
						thread={thread}
						highlighted={
							highlight !== undefined &&
							thread.target.kind !== "board" &&
							thread.target.id === highlight.id
						}
						kind={
							thread.orphaned ? "deleted" : targetKind(board, thread.target)
						}
						label={
							thread.orphaned
								? (thread.targetLabel ?? "?")
								: targetLabel(board, thread.target)
						}
						onFocus={focusFor(thread)}
						onReply={(text) => onReply(thread.id, text)}
						onResolve={(value) => onResolve(thread.id, value)}
					/>
				))}
			</div>
			<form className="composer" onSubmit={submit}>
				<label htmlFor="new-thread" className="composer-target">
					<span className="muted">New thread on</span>
					<span className="kind">{targetKind(board, selection)}</span>
					<span className="target-label">
						{selection.kind === "board" ? "" : targetLabel(board, selection)}
					</span>
				</label>
				<div className="composer-box">
					<textarea
						id="new-thread"
						value={body}
						rows={2}
						placeholder="Write a comment…"
						onChange={(e) => setBody(e.target.value)}
						onKeyDown={submitOnEnter}
					/>
					<button
						type="submit"
						className="icon-button primary"
						disabled={!body.trim()}
						title="Comment (Enter)"
						aria-label="Comment"
					>
						<Icon name="send" />
					</button>
				</div>
			</form>
		</aside>
	)
}
