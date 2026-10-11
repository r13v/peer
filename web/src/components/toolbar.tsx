import type { CardColor } from "../lib/types.ts"

const colors: CardColor[] = ["gray", "yellow", "green", "blue", "purple", "red"]

type Props = {
	canUndo: boolean
	canRedo: boolean
	canColor: boolean
	canArrange: boolean
	canPresent: boolean
	panelOpen: boolean
	onUndo: () => void
	onRedo: () => void
	onAddCard: () => void
	onAddFrame: (slide: boolean) => void
	onColor: (color: CardColor | null) => void
	onArrange: () => void
	onPresent: () => void
	onTogglePanel: () => void
}

export function Toolbar(props: Props) {
	return (
		<div className="toolbar">
			<button type="button" onClick={props.onAddCard} title="Add a card">
				+ Card
			</button>
			<button
				type="button"
				onClick={() => props.onAddFrame(false)}
				title="Add a frame"
			>
				+ Frame
			</button>
			<button
				type="button"
				onClick={() => props.onAddFrame(true)}
				title="Add a 16:9 slide frame"
			>
				+ Slide
			</button>
			<span className="sep" />
			<button
				type="button"
				onClick={props.onUndo}
				disabled={!props.canUndo}
				title="Undo (Cmd/Ctrl+Z)"
			>
				Undo
			</button>
			<button
				type="button"
				onClick={props.onRedo}
				disabled={!props.canRedo}
				title="Redo (Shift+Cmd/Ctrl+Z)"
			>
				Redo
			</button>
			{props.canColor && (
				<span className="swatches">
					<button
						type="button"
						className="swatch color-none"
						title="No color"
						onClick={() => props.onColor(null)}
					/>
					{colors.map((color) => (
						<button
							key={color}
							type="button"
							className={`swatch color-${color}`}
							title={color}
							onClick={() => props.onColor(color)}
						/>
					))}
				</span>
			)}
			{props.canArrange && (
				<button
					type="button"
					onClick={props.onArrange}
					title="Lay out the children of the selected frame"
				>
					Arrange frame
				</button>
			)}
			<span className="sep" />
			<button
				type="button"
				onClick={props.onPresent}
				disabled={!props.canPresent}
				title="Show frames as slides"
			>
				Present
			</button>
			<button
				type="button"
				onClick={props.onTogglePanel}
				aria-pressed={props.panelOpen}
			>
				Threads
			</button>
		</div>
	)
}
