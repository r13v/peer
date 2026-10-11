import type { ResizeParams } from "@xyflow/react"
import { createContext, useContext } from "react"
import type { ThreadCount } from "./board.ts"

// What node and edge components need from the board canvas.
export type BoardActions = {
	boardKey: string
	threadCounts: Map<string, ThreadCount>
	showThreads: (id: string) => void
	setEditing: (id: string | undefined) => void
	updateNode: (id: string, set: Record<string, unknown>) => void
	startResize: () => void
	endResize: (id: string, params: ResizeParams) => void
}

export const BoardContext = createContext<BoardActions | null>(null)

export function useBoardActions(): BoardActions {
	const actions = useContext(BoardContext)
	if (!actions) {
		throw new Error("useBoardActions outside BoardContext")
	}

	return actions
}
