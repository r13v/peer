import { applyPack, errorText, eventsUrl, getBoard, isConflict } from "./api.ts"
import {
	expectFor,
	historyInvalidated,
	invertPack,
	needsReload,
	revOf,
	rewriteExpect,
	type Step,
	touchedIds,
} from "./history.ts"
import type { ApplyResponse, BoardEvent, Op, Pack, Snapshot } from "./types.ts"

export type SyncState = {
	snapshot?: Snapshot
	loadError?: string
	toast?: string
	disconnected: boolean
	canUndo: boolean
	canRedo: boolean
}

const reconnectDelay = 2000

// Keeps one board snapshot in sync with the server and owns the tab's undo history.
// Every pack, reload and undo runs through one queue, so the snapshot a pack is built and
// inverted against is the one the server answered last.
export class BoardSync {
	private readonly key: string
	private readonly onChange: (state: SyncState) => void
	private state: SyncState = {
		disconnected: false,
		canUndo: false,
		canRedo: false,
	}
	private undoSteps: Step[] = []
	private redoSteps: Step[] = []
	private lastRevision = -1
	private queue: Promise<unknown> = Promise.resolve()
	private source: EventSource | undefined
	private timer: ReturnType<typeof setTimeout> | undefined
	private stopped = false
	inFlight = 0

	constructor(key: string, onChange: (state: SyncState) => void) {
		this.key = key
		this.onChange = onChange
	}

	get snapshot(): Snapshot | undefined {
		return this.state.snapshot
	}

	start(): void {
		this.stopped = false
		void this.enqueue(() => this.reload())
		this.connect()
	}

	stop(): void {
		this.stopped = true
		this.source?.close()
		clearTimeout(this.timer)
	}

	notify(text: string | undefined): void {
		this.set({ toast: text })
	}

	// A pack of a user action: it becomes one undo step unless it has no inverse (a reply).
	// `base` is the revision of the snapshot the user saw when the action started (editor
	// opened, drag started), so a concurrent change of the same entity conflicts.
	apply(ops: Op[], base: number): Promise<ApplyResponse | undefined> {
		return this.enqueue(() => this.send(ops, base))
	}

	// Placement and measured sizes: never in the history. `base` is the snapshot the ops were
	// computed from; a conflict reloads the board and the caller recomputes from the new one.
	applyService(ops: Op[], base: Snapshot): Promise<ApplyResponse | undefined> {
		return this.enqueue(() => this.sendService(ops, base))
	}

	undo(): Promise<void> {
		return this.enqueue(() => this.replay("undo"))
	}

	redo(): Promise<void> {
		return this.enqueue(() => this.replay("redo"))
	}

	private enqueue<T>(task: () => Promise<T>): Promise<T> {
		const run = this.queue.then(task, task)
		this.queue = run.catch(() => undefined)

		return run
	}

	private set(patch: Partial<SyncState>): void {
		this.state = {
			...this.state,
			...patch,
			canUndo: this.undoSteps.length > 0,
			canRedo: this.redoSteps.length > 0,
		}
		this.onChange(this.state)
	}

	private adopt(next: Snapshot): void {
		this.lastRevision = Math.max(this.lastRevision, next.board.revision)
		this.set({ snapshot: next, loadError: undefined })
	}

	private clearHistory(): void {
		this.undoSteps = []
		this.redoSteps = []
	}

	private async reload(): Promise<void> {
		try {
			const { board, hash } = await getBoard(this.key)
			if (
				historyInvalidated(
					this.state.snapshot,
					{ board, hash },
					this.lastRevision,
				)
			) {
				this.clearHistory()
			}
			this.adopt({ board, hash })
		} catch (err) {
			if (this.state.snapshot) {
				this.set({ toast: `Reload failed: ${errorText(err)}` })
			} else {
				this.set({ loadError: errorText(err) })
			}
		}
	}

	private async post(pack: Pack): Promise<ApplyResponse> {
		this.inFlight++
		try {
			const res = await applyPack(this.key, pack)
			this.adopt({ board: res.board, hash: res.hash })

			return res
		} finally {
			this.inFlight--
		}
	}

	private async send(
		ops: Op[],
		base: number,
	): Promise<ApplyResponse | undefined> {
		const before = this.state.snapshot?.board
		if (!before || ops.length === 0) {
			return undefined
		}
		try {
			const res = await this.post({ base, ops })
			const inverse = invertPack(before, res.board, touchedIds(res))
			if (inverse.length > 0) {
				this.undoSteps.push({ ops: inverse, expect: expectFor(res) })
				this.redoSteps = []
			}
			this.set({})

			return res
		} catch (err) {
			await this.fail(
				err,
				"The board changed before your edit was saved; it was reloaded.",
			)

			return undefined
		}
	}

	private async sendService(
		ops: Op[],
		base: Snapshot,
	): Promise<ApplyResponse | undefined> {
		if (ops.length === 0) {
			return undefined
		}
		try {
			const res = await this.post({ base: base.board.revision, ops })
			// Our own geometry write must not turn later undo steps into conflicts.
			const touched = touchedIds(res)
			const previous = Object.fromEntries(
				touched.map((id) => [id, revOf(base.board, id)]),
			)
			this.undoSteps = rewriteExpect(
				this.undoSteps,
				touched,
				res.revision,
				previous,
			)
			this.redoSteps = rewriteExpect(
				this.redoSteps,
				touched,
				res.revision,
				previous,
			)
			this.set({})

			return res
		} catch (err) {
			if (isConflict(err)) {
				await this.reload()
			} else {
				this.set({ toast: `Could not save layout: ${errorText(err)}` })
			}

			return undefined
		}
	}

	private async replay(direction: "undo" | "redo"): Promise<void> {
		const from = direction === "undo" ? this.undoSteps : this.redoSteps
		const step = from.pop()
		const before = this.state.snapshot?.board
		if (!step || !before) {
			return
		}
		try {
			const res = await this.post({ expect: step.expect, ops: step.ops })
			const touched = touchedIds(res)
			this.undoSteps = rewriteExpect(this.undoSteps, touched, res.revision)
			this.redoSteps = rewriteExpect(this.redoSteps, touched, res.revision)
			const opposite = {
				ops: invertPack(before, res.board, touched),
				expect: expectFor(res),
			}
			if (direction === "undo") {
				this.redoSteps.push(opposite)
			} else {
				this.undoSteps.push(opposite)
			}
			this.set({})
		} catch (err) {
			await this.fail(
				err,
				`Cannot ${direction}: someone else changed these objects since. The step was dropped.`,
			)
		}
	}

	private async fail(err: unknown, conflictText: string): Promise<void> {
		if (isConflict(err)) {
			this.set({ toast: conflictText })
			await this.reload()

			return
		}
		this.set({ toast: errorText(err) })
	}

	private connect(): void {
		if (this.stopped) {
			return
		}
		const source = new EventSource(eventsUrl(this.key))
		this.source = source
		source.addEventListener("open", () => this.set({ disconnected: false }))
		source.addEventListener("board", (e) =>
			this.onEvent(JSON.parse((e as MessageEvent<string>).data)),
		)
		source.addEventListener("error", () => {
			source.close()
			this.set({ disconnected: true })
			this.timer = setTimeout(() => this.connect(), reconnectDelay)
		})
	}

	private onEvent(event: BoardEvent): void {
		if (event.external) {
			this.clearHistory()
			this.set({})
		}
		void this.enqueue(async () => {
			if (needsReload(this.state.snapshot, event)) {
				await this.reload()
			}
		})
	}
}
