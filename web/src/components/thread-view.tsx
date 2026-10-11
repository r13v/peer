import { type FormEvent, useState } from "react"
import type { Author, Thread } from "../lib/types.ts"

type Props = {
	thread: Thread
	label: string
	onFocus?: () => void
	onReply: (body: string) => void
	onResolve: (resolved: boolean) => void
}

function authorName(author: Author): string {
	return author.roomId ? `${author.role} @ ${author.roomId}` : author.role
}

export function ThreadView({
	thread,
	label,
	onFocus,
	onReply,
	onResolve,
}: Props) {
	const [reply, setReply] = useState("")

	function submit(e: FormEvent) {
		e.preventDefault()
		const body = reply.trim()
		if (!body) {
			return
		}
		onReply(body)
		setReply("")
	}

	return (
		<article className={thread.resolved ? "thread resolved" : "thread"}>
			<header>
				{onFocus ? (
					<button type="button" className="link" onClick={onFocus}>
						{label}
					</button>
				) : (
					<span>{label}</span>
				)}
				<button type="button" onClick={() => onResolve(!thread.resolved)}>
					{thread.resolved ? "Reopen" : "Resolve"}
				</button>
			</header>
			{thread.messages.map((m) => (
				<div key={m.id} className="message">
					<div className="meta">
						<strong>{authorName(m.author)}</strong>{" "}
						<time dateTime={m.createdAt}>
							{new Date(m.createdAt).toLocaleString()}
						</time>
					</div>
					<div className="body">{m.body}</div>
				</div>
			))}
			<form onSubmit={submit}>
				<input
					value={reply}
					placeholder="Reply"
					onChange={(e) => setReply(e.target.value)}
				/>
			</form>
		</article>
	)
}
