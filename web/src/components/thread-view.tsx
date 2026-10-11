import {
	type CSSProperties,
	type FormEvent,
	type KeyboardEvent,
	useState,
} from "react"
import { fullTime, timeAgo } from "../lib/time.ts"
import type { Author, Thread } from "../lib/types.ts"
import { Icon } from "./icon.tsx"

type Props = {
	thread: Thread
	highlighted: boolean
	kind: string
	label: string
	onFocus?: () => void
	onReply: (body: string) => void
	onResolve: (resolved: boolean) => void
}

// Enter sends; Shift+Enter starts a new line.
export function submitOnEnter(e: KeyboardEvent<HTMLTextAreaElement>) {
	if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
		e.preventDefault()
		e.currentTarget.form?.requestSubmit()
	}
}

// A stable hue per role, so each agent keeps its color across threads.
function hue(role: string): number {
	let hash = 0
	for (const char of role) {
		hash = (hash * 31 + char.charCodeAt(0)) % 360
	}

	return hash
}

function Avatar({ author }: { author: Author }) {
	const user = author.role === "user"

	return (
		<span
			className={user ? "avatar user" : "avatar"}
			style={
				user ? undefined : ({ "--hue": hue(author.role) } as CSSProperties)
			}
			aria-hidden="true"
		>
			{author.role.slice(0, 1).toUpperCase()}
		</span>
	)
}

export function ThreadView({
	thread,
	highlighted,
	kind,
	label,
	onFocus,
	onReply,
	onResolve,
}: Props) {
	const [reply, setReply] = useState("")
	// A resolved thread starts collapsed to its first message.
	const [expanded, setExpanded] = useState(!thread.resolved)
	const hidden = thread.messages.length - 1
	const messages =
		expanded || hidden <= 0 ? thread.messages : thread.messages.slice(0, 1)

	function submit(e: FormEvent) {
		e.preventDefault()
		const body = reply.trim()
		if (!body) {
			return
		}
		onReply(body)
		setReply("")
	}

	const targetText = (
		<>
			<span className="kind">{kind}</span>
			<span className="target-label">{label}</span>
		</>
	)

	return (
		<article
			className={[
				"thread",
				thread.resolved && "resolved",
				highlighted && "highlighted",
			]
				.filter(Boolean)
				.join(" ")}
			data-target={
				thread.target.kind === "board" ? undefined : thread.target.id
			}
		>
			<header>
				{onFocus ? (
					<button
						type="button"
						className="thread-target"
						onClick={onFocus}
						title="Show on the board"
					>
						{targetText}
					</button>
				) : (
					<span className="thread-target">{targetText}</span>
				)}
				<button
					type="button"
					className="icon-button"
					onClick={() => onResolve(!thread.resolved)}
					title={thread.resolved ? "Reopen" : "Resolve"}
					aria-label={thread.resolved ? "Reopen" : "Resolve"}
				>
					<Icon name={thread.resolved ? "reopen" : "check"} />
				</button>
			</header>
			{messages.map((m) => (
				<div key={m.id} className="message">
					<Avatar author={m.author} />
					<div className="message-main">
						<div className="meta">
							<strong>{m.author.role}</strong>
							{m.author.roomId && (
								<span className="room-id">{m.author.roomId}</span>
							)}
							<time dateTime={m.createdAt} title={fullTime(m.createdAt)}>
								{timeAgo(m.createdAt)}
							</time>
						</div>
						<div className="body">{m.body}</div>
					</div>
				</div>
			))}
			{hidden > 0 && (
				<button
					type="button"
					className="link more"
					onClick={() => setExpanded(!expanded)}
				>
					{expanded
						? "Collapse"
						: `${hidden} more ${hidden === 1 ? "reply" : "replies"}`}
				</button>
			)}
			{expanded && (
				<form className="reply" onSubmit={submit}>
					<textarea
						value={reply}
						rows={1}
						placeholder="Reply…"
						aria-label="Reply"
						onChange={(e) => setReply(e.target.value)}
						onKeyDown={submitOnEnter}
					/>
				</form>
			)}
		</article>
	)
}
