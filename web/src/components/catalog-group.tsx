import type { CatalogGroup as Group } from "../lib/types.ts"
import { BoardList } from "./board-list.tsx"

function roomTime(room: Group["rooms"][number]): string {
	const started = new Date(room.startedAt).toLocaleString()
	if (!room.endedAt) {
		return `since ${started}`
	}

	return `${started} – ${new Date(room.endedAt).toLocaleString()}`
}

export function CatalogGroup({ group }: { group: Group }) {
	const { checkout, rooms, store, found } = group

	return (
		<section className="group">
			<h2 title={checkout.path}>
				{checkout.name} <span className="muted path">{checkout.path}</span>
			</h2>
			<h3>Rooms</h3>
			{rooms.length === 0 && (
				<p className="muted empty">No rooms with boards</p>
			)}
			{rooms.map((room) => (
				<div key={room.id} className="room">
					<div className="room-head">
						<strong>{room.id}</strong>
						<span className={room.active ? "pill active" : "pill"}>
							{room.active ? "active" : "ended"}
						</span>
						<span className="muted">{roomTime(room)}</span>
					</div>
					<BoardList boards={room.boards} />
				</div>
			))}
			<h3>In store</h3>
			<BoardList boards={store} />
			<h3>Found in workspace</h3>
			<BoardList boards={found} />
		</section>
	)
}
