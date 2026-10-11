import { ReactFlowProvider } from "@xyflow/react"
import { useEffect, useMemo, useState } from "react"
import { BoardSync, type SyncState } from "../lib/board-sync.ts"
import { BoardCanvas } from "./board-canvas.tsx"
import { PeerHint } from "./peer-hint.tsx"

export function BoardPage({ boardKey }: { boardKey: string }) {
	const [state, setState] = useState<SyncState>({
		disconnected: false,
		canUndo: false,
		canRedo: false,
	})
	const sync = useMemo(() => new BoardSync(boardKey, setState), [boardKey])

	useEffect(() => {
		sync.start()

		return () => sync.stop()
	}, [sync])

	const { snapshot, loadError } = state
	if (!snapshot) {
		return (
			<main className="page-message">
				{loadError ? (
					<p className="error">Cannot open the board: {loadError}</p>
				) : (
					<p>Loading the board…</p>
				)}
				<PeerHint />
				<a href="/">All boards</a>
			</main>
		)
	}

	return (
		<ReactFlowProvider>
			<BoardCanvas
				boardKey={boardKey}
				sync={sync}
				state={state}
				snapshot={snapshot}
			/>
		</ReactFlowProvider>
	)
}
