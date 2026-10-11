import type {
	ApplyResponse,
	BoardError,
	BoardRef,
	Catalog,
	Pack,
	Snapshot,
} from "./types.ts"

class ApiError extends Error {
	readonly detail: BoardError | undefined

	constructor(detail: BoardError | undefined, message: string) {
		super(message)
		this.detail = detail
	}
}

export function isConflict(err: unknown): boolean {
	return err instanceof ApiError && err.detail?.error === "conflict"
}

export function describeError(detail: BoardError): string {
	switch (detail.error) {
		case "conflict":
			return `conflict on ${detail.conflicts.map((c) => c.id).join(", ")}`
		case "invalid":
			return detail.problems.map((p) => `${p.path}: ${p.message}`).join("; ")
		case "unsupportedVersion":
			return `board format ${detail.formatVersion} is newer than this peer; update peer`
		case "tooLarge":
			return `file is larger than ${detail.limit} bytes`
		default:
			return detail.message
	}
}

export function errorText(err: unknown): string {
	if (err instanceof Error) {
		return err.message
	}

	return String(err)
}

function isBoardError(value: unknown): value is BoardError {
	return typeof value === "object" && value !== null && "error" in value
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
	const res = await fetch(path, init)
	const text = await res.text()
	let body: unknown
	try {
		body = text ? JSON.parse(text) : undefined
	} catch {
		body = undefined
	}
	if (!res.ok) {
		const detail = isBoardError(body) ? body : undefined
		const message = detail ? describeError(detail) : `HTTP ${res.status}`
		throw new ApiError(detail, message)
	}

	return body as T
}

// The server rejects a POST without X-Peer; the browser adds the Origin header itself.
function post<T>(
	path: string,
	body: BodyInit,
	contentType: string,
): Promise<T> {
	return request<T>(path, {
		method: "POST",
		headers: { "X-Peer": "1", "Content-Type": contentType },
		body,
	})
}

function postJSON<T>(path: string, body: unknown): Promise<T> {
	return post<T>(path, JSON.stringify(body), "application/json")
}

export function getCatalog(): Promise<Catalog> {
	return request<Catalog>("/api/catalog")
}

export function rescan(): Promise<Catalog> {
	return postJSON<Catalog>("/api/rescan", {})
}

export function getBoard(key: string): Promise<Snapshot & { ref: BoardRef }> {
	return request(`/api/boards/${key}`)
}

export function applyPack(key: string, pack: Pack): Promise<ApplyResponse> {
	return postJSON<ApplyResponse>(`/api/boards/${key}/apply`, pack)
}

export function uploadAsset(
	key: string,
	file: File,
): Promise<{ asset: string; width: number; height: number }> {
	return post(`/api/boards/${key}/assets`, file, file.type)
}

export function eventsUrl(key: string): string {
	return `/api/boards/${key}/events`
}

export function assetUrl(key: string, asset: string): string {
	const file = asset.replace(/^assets\//, "")

	return `/b/${key}/assets/${encodeURIComponent(file)}`
}
