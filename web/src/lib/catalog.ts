import type { BoardRef, Catalog } from "./types.ts"

export function dirName(path: string): string {
	return path.split("/").filter(Boolean).at(-1) ?? path
}

function matches(ref: BoardRef, needle: string): boolean {
	const haystack = [ref.title ?? "", dirName(ref.path), ref.path]
		.join("\n")
		.toLowerCase()

	return haystack.includes(needle)
}

// Keeps the boards whose title, directory name or path contains the query; drops rooms and
// groups left without boards. An empty query keeps every board.
export function filterCatalog(catalog: Catalog, query: string): Catalog {
	const needle = query.trim().toLowerCase()
	const keep = (refs: BoardRef[]) =>
		needle ? refs.filter((ref) => matches(ref, needle)) : refs
	const groups = catalog.groups
		.map((group) => ({
			...group,
			rooms: group.rooms
				.map((room) => ({ ...room, boards: keep(room.boards) }))
				.filter((room) => room.boards.length > 0),
			store: keep(group.store),
			found: keep(group.found),
		}))
		.filter(
			(group) =>
				group.rooms.length + group.store.length + group.found.length > 0,
		)

	return { ...catalog, groups, outside: keep(catalog.outside) }
}

// Distinct boards: a board linked to several rooms shows under each of them.
export function boardCount(catalog: Catalog): number {
	const keys = new Set(catalog.outside.map((ref) => ref.key))
	for (const group of catalog.groups) {
		const refs = [...group.store, ...group.found]
		for (const ref of [...refs, ...group.rooms.flatMap((r) => r.boards)]) {
			keys.add(ref.key)
		}
	}

	return keys.size
}
