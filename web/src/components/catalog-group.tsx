import { timeRange } from "../lib/time.ts"
import type { BoardRef, CatalogGroup as Group } from "../lib/types.ts"
import { BoardList } from "./board-list.tsx"

function Section({ title, boards }: { title: string; boards: BoardRef[] }) {
	if (boards.length === 0) {
		return null
	}

	return (
		<div className="room">
			<div className="room-head">
				<h3>{title}</h3>
			</div>
			<BoardList boards={boards} />
		</div>
	)
}

export function CatalogGroup({ group }: { group: Group }) {
	const { checkout, rooms, store, found } = group

	return (
		<section className="group">
			<header className="group-head">
				<h2>{checkout.name}</h2>
				<span className="path" dir="rtl" title={checkout.path}>
					<bdi>{checkout.path}</bdi>
				</span>
			</header>
			{rooms.map((room) => (
				<div key={room.id} className="room">
					<div className="room-head">
						<span
							className={room.active ? "status active" : "status"}
							title={room.active ? "Active room" : "Ended room"}
						/>
						<h3>{room.id}</h3>
						<span className="muted">
							{timeRange(room.startedAt, room.endedAt)}
						</span>
					</div>
					<BoardList boards={room.boards} />
				</div>
			))}
			<Section title="In store" boards={store} />
			<Section title="Found in workspace" boards={found} />
		</section>
	)
}
