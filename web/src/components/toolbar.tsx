import type { CardColor } from "../lib/types.ts"
import { Icon } from "./icon.tsx"

const colors: CardColor[] = ["gray", "yellow", "green", "blue", "purple", "red"]

type Props = {
	canUndo: boolean
	canRedo: boolean
	canColor: boolean
	canArrange: boolean
	canPresent: boolean
	panelOpen: boolean
	openThreads: number
	onUndo: () => void
	onRedo: () => void
	onAddCard: () => void
	onAddFrame: () => void
	onColor: (color: CardColor | null) => void
	onArrange: () => void
	onPresent: () => void
	onTogglePanel: () => void
}

export function Toolbar(props: Props) {
	return (
		<div className="toolbar">
			<div className="tool-group">
				<button type="button" onClick={props.onAddCard} title="Add a card">
					<Icon name="card" />
					Card
				</button>
				<button type="button" onClick={props.onAddFrame} title="Add a frame">
					<Icon name="frame" />
					Frame
				</button>
			</div>
			<div className="tool-group">
				<button
					type="button"
					className="icon-button"
					onClick={props.onUndo}
					disabled={!props.canUndo}
					title="Undo (Cmd/Ctrl+Z)"
					aria-label="Undo"
				>
					<Icon name="undo" />
				</button>
				<button
					type="button"
					className="icon-button"
					onClick={props.onRedo}
					disabled={!props.canRedo}
					title="Redo (Shift+Cmd/Ctrl+Z)"
					aria-label="Redo"
				>
					<Icon name="redo" />
				</button>
			</div>
			{props.canColor && (
				<div className="tool-group swatches">
					<button
						type="button"
						className="swatch"
						style={{ background: "var(--card-none)" }}
						title="No color"
						aria-label="No color"
						onClick={() => props.onColor(null)}
					/>
					{colors.map((color) => (
						<button
							key={color}
							type="button"
							className="swatch"
							// Inline, so the toolbar's transparent button style does not hide it.
							style={{ background: `var(--card-${color})` }}
							title={color}
							aria-label={color}
							onClick={() => props.onColor(color)}
						/>
					))}
				</div>
			)}
			{props.canArrange && (
				<button
					type="button"
					onClick={props.onArrange}
					title="Lay out the children of the selected frame"
				>
					<Icon name="arrange" />
					Arrange
				</button>
			)}
			<div className="tool-group end">
				<button
					type="button"
					onClick={props.onPresent}
					disabled={!props.canPresent}
					title="Show frames as slides"
				>
					<Icon name="play" />
					Present
				</button>
				<button
					type="button"
					onClick={props.onTogglePanel}
					aria-pressed={props.panelOpen}
					title="Show or hide comment threads"
				>
					<Icon name="message" />
					Threads
					{props.openThreads > 0 && (
						<span className="count">{props.openThreads}</span>
					)}
				</button>
			</div>
		</div>
	)
}
