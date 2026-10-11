import {
	applyEdgeChanges,
	applyNodeChanges,
	Background,
	type Connection,
	ConnectionMode,
	Controls,
	type EdgeChange,
	MarkerType,
	MiniMap,
	type NodeChange,
	type OnConnectEnd,
	ReactFlow,
	type ResizeParams,
	useReactFlow,
	type XYPosition,
} from "@xyflow/react"
import { type DragEvent, useEffect, useMemo, useRef, useState } from "react"
import { errorText, uploadAsset } from "../lib/api.ts"
import {
	threadCounts as countThreads,
	depthOf,
	isInside,
	isPlaced,
	nodeMap,
	targetFor,
} from "../lib/board.ts"
import { type BoardActions, BoardContext } from "../lib/board-context.ts"
import type { BoardSync, SyncState } from "../lib/board-sync.ts"
import { arrangeOps, changedSet, deleteOps } from "../lib/edit.ts"
import {
	type FlowEdge,
	type FlowNode,
	toFlowEdges,
	toFlowNodes,
	withSides,
} from "../lib/flow.ts"
import { fontsSettled, loadFonts } from "../lib/fonts.ts"
import { placementOps, planPlacement, settleOps } from "../lib/layout.ts"
import type {
	Board,
	CardColor,
	FrameNode,
	NewNode,
	Op,
	Snapshot,
	Target,
} from "../lib/types.ts"
import { BoardEdgeView } from "./board-edge.tsx"
import { CardNodeView } from "./card-node.tsx"
import { FrameNodeView } from "./frame-node.tsx"
import { Icon } from "./icon.tsx"
import { ImageNodeView } from "./image-node.tsx"
import { SearchBox } from "./search-box.tsx"
import { ThreadPanel } from "./thread-panel.tsx"
import { Toolbar } from "./toolbar.tsx"

const nodeTypes = {
	card: CardNodeView,
	image: ImageNodeView,
	frame: FrameNodeView,
}
const edgeTypes = { board: BoardEdgeView }
const defaultEdgeOptions = { markerEnd: { type: MarkerType.ArrowClosed } }

function minimapClass(flowNode: FlowNode): string {
	const { node } = flowNode.data

	return node.type === "card"
		? `mm-card color-${node.color ?? "none"}`
		: `mm-${node.type}`
}

const measureDelay = 300 // ms after the last render before sizes are written
const toastTimeout = 6000
const imageMaxWidth = 480
const dropStep = 24
const imageTypes = ["image/png", "image/jpeg", "image/gif", "image/webp"]
const newCard = { w: 240, h: 80 }
const newFrame = { w: 480, h: 320 }

type Props = {
	boardKey: string
	sync: BoardSync
	state: SyncState
	snapshot: Snapshot
}

function selectedIds(items: { id: string; selected?: boolean }[]): Set<string> {
	return new Set(items.filter((i) => i.selected).map((i) => i.id))
}

// Rendered sizes in board units (independent of zoom), rounded; only for flow nodes built
// from this board, so a size never belongs to an older snapshot.
function measuredSizes(
	nodes: FlowNode[],
	board: Board,
): Map<string, { w: number; h: number }> {
	const current = new Set(board.nodes)
	const sizes = new Map<string, { w: number; h: number }>()
	for (const flowNode of nodes) {
		const { measured, data } = flowNode
		if (!current.has(data.node) || !measured?.width || !measured.height) {
			continue
		}
		sizes.set(flowNode.id, {
			w: Math.round(measured.width),
			h: Math.round(measured.height),
		})
	}

	return sizes
}

function isTyping(target: EventTarget | null): boolean {
	return (
		target instanceof HTMLElement &&
		(target.isContentEditable || ["INPUT", "TEXTAREA"].includes(target.tagName))
	)
}

function slideFrames(board: Board): FrameNode[] {
	return board.nodes
		.filter((n): n is FrameNode => n.type === "frame")
		.sort((a, b) => a.order - b.order)
}

export function BoardCanvas({ boardKey, sync, state, snapshot }: Props) {
	const { board } = snapshot
	const flow = useReactFlow<FlowNode, FlowEdge>()
	const placement = useMemo(() => planPlacement(board), [board])
	const threadCounts = useMemo(() => countThreads(board), [board])
	const [nodes, setNodes] = useState<FlowNode[]>([])
	const [edges, setEdges] = useState<FlowEdge[]>([])
	const [editingId, setEditingId] = useState<string>()
	const [panelOpen, setPanelOpen] = useState(true)
	const [threadFocus, setThreadFocus] = useState<{ id: string }>()
	const [slide, setSlide] = useState<number>()
	const [fontsReady, setFontsReady] = useState(false)
	const interacting = useRef(false)
	const placedHash = useRef<string>(undefined)
	const measuredKey = useRef<string>(undefined)
	const wrapper = useRef<HTMLDivElement>(null)
	const gestureBase = useRef<number>(undefined)
	const boardRef = useRef(board)
	boardRef.current = board

	const frames = useMemo(() => slideFrames(board), [board])
	const shownEdges = useMemo(() => withSides(edges, nodes), [edges, nodes])
	const selectedNodes = nodes.filter((n) => n.selected)
	const selectedEdges = edges.filter((e) => e.selected)
	const single =
		selectedNodes.length + selectedEdges.length === 1
			? (selectedNodes[0] ?? selectedEdges[0])
			: undefined
	const selection: Target = (single && targetFor(board, single.id)) || {
		kind: "board",
	}
	const selectedFrame =
		selectedNodes.length === 1 && selectedNodes[0].data.node.type === "frame"
	const selectedCards = selectedNodes.filter((n) => n.data.node.type === "card")

	useEffect(() => {
		document.title = `${board.title} · peer`
	}, [board.title])

	useEffect(() => {
		setNodes((prev) => toFlowNodes(board, placement, selectedIds(prev)))
		setEdges((prev) => toFlowEdges(board, selectedIds(prev)))
	}, [board, placement])

	useEffect(() => {
		const done = () => setFontsReady(true)
		loadFonts().then(done, done)
	}, [])

	// Placement of unplaced nodes: they render invisibly first; once the font is loaded and
	// every unplaced card and image is measured, dagre lays them out with the real sizes. One
	// service pack per snapshot with positions and sizes, sent only when complete.
	useEffect(() => {
		const { unplaced } = placement
		if (
			!fontsReady ||
			unplaced.length === 0 ||
			placedHash.current === snapshot.hash
		) {
			return
		}
		const timer = setTimeout(() => {
			if (!fontsSettled()) {
				return
			}
			const measured = measuredSizes(nodes, board)
			const stored = nodeMap(board)
			const isMeasured = (id: string) =>
				stored.get(id)?.type === "frame" || measured.has(id)
			if (!unplaced.every(isMeasured)) {
				return
			}
			const final = planPlacement(board, measured)
			if (!unplaced.every((id) => final.positions.has(id))) {
				return
			}
			placedHash.current = snapshot.hash
			void sync.applyService(placementOps(final), snapshot)
		}, measureDelay)

		return () => clearTimeout(timer)
	}, [nodes, board, placement, snapshot, sync, fontsReady])

	// Measured sizes: written only when they differ from the stored ones, never while the user
	// drags, resizes or edits, while a pack is in flight or a placement is pending (it may fit
	// frames); one attempt per snapshot and size set.
	useEffect(() => {
		const placing = placement.unplaced.length > 0
		const timer = setTimeout(() => {
			const busy = editingId || interacting.current || sync.inFlight > 0
			if (placing || busy || !fontsReady || !fontsSettled()) {
				return
			}
			const measured = measuredSizes(nodes, board)
			const sets = new Map<string, Record<string, unknown>>()
			for (const [id, size] of measured) {
				const node = board.nodes.find((n) => n.id === id)
				const set = node && isPlaced(node) && changedSet(node, size)
				if (set) {
					sets.set(id, set)
				}
			}
			// A card that grew must not cover the nodes below it.
			for (const op of settleOps(board, measured)) {
				if (op.op === "update") {
					sets.set(op.id, { ...sets.get(op.id), ...op.set })
				}
			}
			const ops = [...sets].map(([id, set]): Op => ({ op: "update", id, set }))
			const key = `${snapshot.hash}:${JSON.stringify(ops)}`
			if (ops.length === 0 || measuredKey.current === key) {
				return
			}
			measuredKey.current = key
			void sync.applyService(ops, snapshot)
		}, measureDelay)

		return () => clearTimeout(timer)
	}, [nodes, board, snapshot, placement, editingId, sync, fontsReady])

	useEffect(() => {
		if (!state.toast) {
			return
		}
		const timer = setTimeout(() => sync.notify(undefined), toastTimeout)

		return () => clearTimeout(timer)
	}, [state.toast, sync])

	useEffect(() => {
		const frame = slide === undefined ? undefined : frames[slide]
		if (frame) {
			void flow.fitView({
				nodes: [{ id: frame.id }],
				duration: 300,
				padding: 0.05,
			})
		}
	}, [slide, frames, flow])

	// Packs of a click carry the revision shown now; a drag, resize or text edit carries the one
	// shown when it started, so a concurrent change of the same entity conflicts.
	function apply(ops: Op[], base = boardRef.current.revision) {
		void sync.apply(ops, base)
	}

	function beginGesture() {
		gestureBase.current = boardRef.current.revision
	}

	function absoluteOf(id: string): XYPosition & { w: number; h: number } {
		const internal = flow.getInternalNode(id)
		const position = internal?.internals.positionAbsolute ?? { x: 0, y: 0 }

		return {
			...position,
			w: internal?.measured.width ?? 0,
			h: internal?.measured.height ?? 0,
		}
	}

	// The innermost frame under a board point, excluding `exclude` and its content.
	function frameAt(point: XYPosition, exclude?: string): FrameNode | undefined {
		const stored = nodeMap(board)
		let best: FrameNode | undefined
		for (const frame of frames) {
			const excluded =
				exclude !== undefined &&
				(frame.id === exclude || isInside(stored, frame.id, exclude))
			const r = absoluteOf(frame.id)
			const contains =
				point.x >= r.x &&
				point.x <= r.x + r.w &&
				point.y >= r.y &&
				point.y <= r.y + r.h
			const deeper =
				!best || depthOf(stored, frame.id) > depthOf(stored, best.id)
			if (!excluded && contains && deeper) {
				best = frame
			}
		}

		return best
	}

	function relativeTo(
		frame: FrameNode | undefined,
		point: XYPosition,
	): XYPosition {
		const origin = frame ? absoluteOf(frame.id) : { x: 0, y: 0 }

		return {
			x: Math.round(point.x - origin.x),
			y: Math.round(point.y - origin.y),
		}
	}

	function viewCenter(): XYPosition {
		const rect = wrapper.current?.getBoundingClientRect()
		if (!rect) {
			return { x: 0, y: 0 }
		}

		return flow.screenToFlowPosition({
			x: rect.left + rect.width / 2,
			y: rect.top + rect.height / 2,
		})
	}

	function zRange(): { min: number; max: number } {
		const zs = board.nodes.map((n) => n.z ?? 0)

		return { min: Math.min(0, ...zs), max: Math.max(0, ...zs) }
	}

	function addAtCenter(node: NewNode, size: { w: number; h: number }) {
		const center = viewCenter()
		const corner = { x: center.x - size.w / 2, y: center.y - size.h / 2 }
		const frame = frameAt(center)
		const p = relativeTo(frame, corner)
		const added: NewNode = { ...node, x: p.x, y: p.y, parentId: frame?.id }
		apply([{ op: "add", node: added }])
	}

	function addCard() {
		addAtCenter(
			{ type: "card", text: "New card", w: newCard.w, z: zRange().max + 1 },
			newCard,
		)
	}

	// A frame goes under existing nodes so it does not hide them.
	function addFrame() {
		const order = Math.max(0, ...frames.map((f) => f.order)) + 1
		const frame: NewNode = {
			type: "frame",
			title: "New frame",
			order,
			...newFrame,
			z: zRange().min - 1,
		}
		addAtCenter(frame, newFrame)
	}

	function deleteSelection(cascade: boolean) {
		const nodeIds = selectedNodes.map((n) => n.id)
		const edgeIds = selectedEdges.map((e) => e.id)
		const hasFrame = selectedNodes.some((n) => n.data.node.type === "frame")
		if (nodeIds.length + edgeIds.length === 0) {
			return
		}
		if (
			cascade &&
			hasFrame &&
			!window.confirm("Delete the selected frames with everything inside them?")
		) {
			return
		}
		apply(deleteOps(board, nodeIds, edgeIds, cascade && hasFrame))
	}

	// A dropped node moves into the innermost frame under its center, or out to the board.
	function onNodeDragStop(_: unknown, _node: FlowNode, dragged: FlowNode[]) {
		interacting.current = false
		const stored = nodeMap(board)
		const draggedIds = dragged.map((n) => n.id)
		const roots = dragged.filter(
			(n) => !draggedIds.some((other) => isInside(stored, n.id, other)),
		)
		const ops: Op[] = []
		for (const flowNode of roots) {
			const node = stored.get(flowNode.id)
			if (!node) {
				continue
			}
			const abs = absoluteOf(node.id)
			const frame = frameAt(
				{ x: abs.x + abs.w / 2, y: abs.y + abs.h / 2 },
				node.id,
			)
			const p = relativeTo(frame, abs)
			const set = changedSet(node, {
				parentId: frame?.id ?? null,
				x: p.x,
				y: p.y,
			})
			if (set) {
				ops.push({ op: "update", id: node.id, set })
			}
		}
		apply(ops, gestureBase.current)
	}

	function endResize(id: string, params: ResizeParams) {
		interacting.current = false
		const node = boardRef.current.nodes.find((n) => n.id === id)
		const next = {
			x: Math.round(params.x),
			y: Math.round(params.y),
			w: Math.round(params.width),
			h: Math.round(params.height),
		}
		const set = node && changedSet(node, next)
		if (set) {
			apply([{ op: "update", id, set }], gestureBase.current)
		}
	}

	function onConnect(connection: Connection) {
		apply([
			{ op: "add", edge: { from: connection.source, to: connection.target } },
		])
	}

	// Releasing a new edge anywhere on a node connects to it, not only on a handle.
	const onConnectEnd: OnConnectEnd = (event, state) => {
		if (state.isValid || !state.fromNode) {
			return
		}
		const point = "changedTouches" in event ? event.changedTouches[0] : event
		const target = document
			.elementFromPoint(point.clientX, point.clientY)
			?.closest<HTMLElement>(".react-flow__node")?.dataset.id
		if (target && target !== state.fromNode.id) {
			onConnect({
				source: state.fromNode.id,
				target,
				sourceHandle: null,
				targetHandle: null,
			})
		}
	}

	function onReconnect(old: FlowEdge, connection: Connection) {
		const set = changedSet(
			{ from: old.source, to: old.target },
			{ from: connection.source, to: connection.target },
		)
		if (set) {
			apply([{ op: "update", id: old.id, set }])
		}
	}

	function onEdgeDoubleClick(_: unknown, edge: FlowEdge) {
		const label = window.prompt("Edge label", edge.data?.edge.label ?? "")
		if (label !== null) {
			apply([
				{ op: "update", id: edge.id, set: { label: label.trim() || null } },
			])
		}
	}

	async function onDrop(e: DragEvent) {
		const files = [...e.dataTransfer.files].filter((f) =>
			imageTypes.includes(f.type),
		)
		if (files.length === 0) {
			return
		}
		e.preventDefault()
		const point = flow.screenToFlowPosition({ x: e.clientX, y: e.clientY })
		const frame = frameAt(point)
		const at = relativeTo(frame, point)
		try {
			const uploads = await Promise.all(
				files.map((file) => uploadAsset(boardKey, file)),
			)
			const z = zRange().max + 1
			const ops = uploads.map((upload, i): Op => {
				const scale = Math.min(1, imageMaxWidth / upload.width)
				const node: NewNode = {
					type: "image",
					asset: upload.asset,
					parentId: frame?.id,
					x: at.x + i * dropStep,
					y: at.y + i * dropStep,
					w: Math.round(upload.width * scale),
					h: Math.round(upload.height * scale),
					z: z + i,
				}

				return { op: "add", node }
			})
			apply(ops)
		} catch (err) {
			sync.notify(`Image upload failed: ${errorText(err)}`)
		}
	}

	function select(id: string) {
		setNodes((prev) => prev.map((n) => ({ ...n, selected: n.id === id })))
		setEdges((prev) =>
			prev.map((edge) => ({ ...edge, selected: edge.id === id })),
		)
	}

	function focusNode(id: string) {
		select(id)
		const edge = board.edges.find((candidate) => candidate.id === id)
		const ids = edge ? [edge.from, edge.to] : [id]
		void flow.fitView({
			nodes: ids.map((nodeId) => ({ id: nodeId })),
			duration: 300,
			maxZoom: 1.5,
			padding: 0.4,
		})
	}

	function setColor(color: CardColor | null) {
		apply(
			selectedCards.map(
				(n): Op => ({ op: "update", id: n.id, set: { color } }),
			),
		)
	}

	function arrange() {
		const node = selectedNodes[0]?.data.node
		if (node?.type === "frame") {
			apply(arrangeOps(board, node))
		}
	}

	function stepSlide(delta: number) {
		setSlide((current) =>
			Math.min(frames.length - 1, Math.max(0, (current ?? 0) + delta)),
		)
	}

	function onKey(e: KeyboardEvent) {
		if (isTyping(e.target)) {
			return
		}
		if (slide !== undefined) {
			const forward = ["ArrowRight", "ArrowDown", " ", "PageDown"].includes(
				e.key,
			)
			const back = ["ArrowLeft", "ArrowUp", "PageUp"].includes(e.key)
			if (e.key === "Escape") {
				setSlide(undefined)
			} else if (forward || back) {
				e.preventDefault()
				stepSlide(forward ? 1 : -1)
			}

			return
		}
		const mod = e.metaKey || e.ctrlKey
		if (mod && e.key.toLowerCase() === "z") {
			e.preventDefault()
			void (e.shiftKey ? sync.redo() : sync.undo())
		} else if (e.key === "Delete" || e.key === "Backspace") {
			e.preventDefault()
			deleteSelection(e.shiftKey)
		}
	}

	const keyHandler = useRef(onKey)
	keyHandler.current = onKey
	useEffect(() => {
		const listener = (e: KeyboardEvent) => keyHandler.current(e)
		window.addEventListener("keydown", listener)

		return () => window.removeEventListener("keydown", listener)
	}, [])

	const selectRef = useRef(select)
	selectRef.current = select
	const beginGestureRef = useRef(beginGesture)
	const endResizeRef = useRef(endResize)
	endResizeRef.current = endResize
	const actions = useMemo(
		(): BoardActions => ({
			boardKey,
			threadCounts,
			// A new object each time, so a second click on the same badge scrolls again.
			showThreads: (id) => {
				selectRef.current(id)
				setPanelOpen(true)
				setThreadFocus({ id })
			},
			setEditing: (id) => {
				if (id) {
					beginGestureRef.current()
				}
				setEditingId(id)
			},
			updateNode: (id, set) =>
				void sync.apply(
					[{ op: "update", id, set }],
					gestureBase.current ?? boardRef.current.revision,
				),
			startResize: () => {
				interacting.current = true
				beginGestureRef.current()
			},
			endResize: (id, params) => endResizeRef.current(id, params),
		}),
		[boardKey, threadCounts, sync],
	)

	const presenting = slide !== undefined

	return (
		<BoardContext value={actions}>
			<div className={presenting ? "board-page presenting" : "board-page"}>
				<header className="board-header">
					<a href="/" className="home-link" title="All boards">
						<Icon name="back" />
						Boards
					</a>
					<span className="divider" />
					<h1 title={board.title}>{board.title}</h1>
					<span className="chip" title="Board revision">
						rev {board.revision}
					</span>
					<Toolbar
						canUndo={state.canUndo}
						canRedo={state.canRedo}
						canColor={selectedCards.length > 0}
						canArrange={selectedFrame}
						canPresent={frames.length > 0}
						panelOpen={panelOpen}
						openThreads={board.threads.filter((t) => !t.resolved).length}
						onUndo={() => void sync.undo()}
						onRedo={() => void sync.redo()}
						onAddCard={addCard}
						onAddFrame={addFrame}
						onColor={setColor}
						onArrange={arrange}
						onPresent={() => setSlide(0)}
						onTogglePanel={() => setPanelOpen((open) => !open)}
					/>
					<SearchBox board={board} onPick={focusNode} />
				</header>
				{state.disconnected && (
					<div className="banner">
						Disconnected from peer, reconnecting. If it does not come back,
						start <code>peer</code>.
					</div>
				)}
				<div className="board-body">
					<div
						className="canvas"
						role="application"
						ref={wrapper}
						onDragOver={(e) => e.preventDefault()}
						onDrop={(e) => void onDrop(e)}
					>
						<ReactFlow<FlowNode, FlowEdge>
							nodes={nodes}
							edges={shownEdges}
							nodeTypes={nodeTypes}
							edgeTypes={edgeTypes}
							defaultEdgeOptions={defaultEdgeOptions}
							onNodesChange={(changes: NodeChange<FlowNode>[]) =>
								setNodes((prev) => applyNodeChanges(changes, prev))
							}
							onEdgesChange={(changes: EdgeChange<FlowEdge>[]) =>
								setEdges((prev) => applyEdgeChanges(changes, prev))
							}
							onNodeDragStart={() => {
								interacting.current = true
								beginGesture()
							}}
							onNodeDragStop={onNodeDragStop}
							onConnect={onConnect}
							onConnectEnd={onConnectEnd}
							connectionMode={ConnectionMode.Loose}
							onReconnect={onReconnect}
							onEdgeDoubleClick={onEdgeDoubleClick}
							deleteKeyCode={null}
							colorMode="system"
							minZoom={0.05}
							fitView
						>
							{!presenting && (
								<MiniMap pannable zoomable nodeClassName={minimapClass} />
							)}
							{!presenting && <Controls />}
							<Background gap={20} size={1.2} />
						</ReactFlow>
					</div>
					{panelOpen && !presenting && (
						<ThreadPanel
							board={board}
							selection={selection}
							onComment={(target, body) =>
								apply([{ op: "comment", target, body }])
							}
							onReply={(thread, body) => apply([{ op: "reply", thread, body }])}
							onResolve={(thread, resolved) =>
								apply([{ op: "resolve", thread, resolved }])
							}
							onFocus={focusNode}
							focus={threadFocus}
						/>
					)}
				</div>
				{presenting && (
					<div className="slide-hint">
						<button
							type="button"
							className="icon-button"
							onClick={() => stepSlide(-1)}
							disabled={slide === 0}
							aria-label="Previous slide"
						>
							<Icon name="back" />
						</button>
						<span className="slide-title">{frames[slide]?.title}</span>
						<span className="muted">
							{slide + 1} / {frames.length}
						</span>
						<button
							type="button"
							className="icon-button"
							onClick={() => stepSlide(1)}
							disabled={slide === frames.length - 1}
							aria-label="Next slide"
						>
							<Icon name="next" />
						</button>
						<button
							type="button"
							className="icon-button"
							onClick={() => setSlide(undefined)}
							title="Exit (Esc)"
							aria-label="Exit presentation"
						>
							<Icon name="close" />
						</button>
					</div>
				)}
				{state.toast && (
					<button
						type="button"
						className="toast"
						onClick={() => sync.notify(undefined)}
					>
						{state.toast}
					</button>
				)}
			</div>
		</BoardContext>
	)
}
