import { type KeyboardEvent, useRef, useState } from "react"
import { useBoardActions } from "../lib/board-context.ts"

type Props = {
	id: string
	value: string
	placeholder: string
	className: string
	multiline?: boolean
	onCommit: (text: string) => void
}

// Double click edits; blur or Enter (Cmd/Ctrl+Enter when multiline) saves, Escape cancels.
export function InlineText({
	id,
	value,
	placeholder,
	className,
	multiline,
	onCommit,
}: Props) {
	const { setEditing } = useBoardActions()
	const [draft, setDraft] = useState<string>()
	const closed = useRef(false)

	function start() {
		closed.current = false
		setDraft(value)
		setEditing(id)
	}

	function finish(save: boolean) {
		if (closed.current) {
			return
		}
		closed.current = true
		if (save && draft !== undefined && draft !== value) {
			onCommit(draft)
		}
		setDraft(undefined)
		setEditing(undefined)
	}

	function onKeyDown(e: KeyboardEvent) {
		e.stopPropagation()
		const submit = e.key === "Enter" && (!multiline || e.metaKey || e.ctrlKey)
		if (e.key === "Escape") {
			finish(false)
		} else if (submit) {
			e.preventDefault()
			finish(true)
		}
	}

	if (draft === undefined) {
		return (
			// biome-ignore lint/a11y/noStaticElementInteractions: double click on a board node opens its editor
			<div className={className} onDoubleClick={start}>
				{value || <span className="placeholder">{placeholder}</span>}
			</div>
		)
	}
	const editorClass = `${className} editor nodrag nowheel nopan`

	return multiline ? (
		<textarea
			className={editorClass}
			value={draft}
			// biome-ignore lint/a11y/noAutofocus: the editor opens on an explicit double click
			autoFocus
			onChange={(e) => setDraft(e.target.value)}
			onBlur={() => finish(true)}
			onKeyDown={onKeyDown}
		/>
	) : (
		<input
			className={editorClass}
			value={draft}
			// biome-ignore lint/a11y/noAutofocus: the editor opens on an explicit double click
			autoFocus
			onChange={(e) => setDraft(e.target.value)}
			onBlur={() => finish(true)}
			onKeyDown={onKeyDown}
		/>
	)
}
