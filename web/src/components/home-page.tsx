import { type FormEvent, useEffect, useState } from "react"
import { errorText, getCatalog, rescan } from "../lib/api.ts"
import { base64url } from "../lib/base64url.ts"
import { boardCount, filterCatalog } from "../lib/catalog.ts"
import type { Catalog } from "../lib/types.ts"
import { BoardList } from "./board-list.tsx"
import { CatalogGroup } from "./catalog-group.tsx"
import { Icon } from "./icon.tsx"
import { PeerHint } from "./peer-hint.tsx"

export function HomePage() {
	const [catalog, setCatalog] = useState<Catalog>()
	const [error, setError] = useState<string>()
	const [query, setQuery] = useState("")
	const [path, setPath] = useState("")
	const [pathError, setPathError] = useState<string>()
	const [scanning, setScanning] = useState(false)

	useEffect(() => {
		document.title = "Boards · peer"
		getCatalog().then(setCatalog, (err: unknown) => setError(errorText(err)))
	}, [])

	async function refresh() {
		setScanning(true)
		try {
			setCatalog(await rescan())
			setError(undefined)
		} catch (err) {
			setError(errorText(err))
		} finally {
			setScanning(false)
		}
	}

	function openPath(e: FormEvent) {
		e.preventDefault()
		const trimmed = path.trim().replace(/\/+$/, "")
		if (!trimmed.startsWith("/")) {
			setPathError("Enter an absolute path to a *.board directory.")
			return
		}
		window.location.href = `/b/${base64url(trimmed)}`
	}

	const shown = catalog && filterCatalog(catalog, query)

	return (
		<main className="home">
			<header className="home-header">
				<div className="home-title">
					<span className="logo">peer</span>
					<h1>Boards</h1>
					{shown && <span className="chip">{boardCount(shown)}</span>}
				</div>
				<label className="field search-field">
					<Icon name="search" />
					<input
						type="search"
						placeholder="Search title, directory or path"
						aria-label="Search boards"
						value={query}
						onChange={(e) => setQuery(e.target.value)}
					/>
				</label>
				<button
					type="button"
					onClick={() => void refresh()}
					disabled={scanning}
					title="Scan the workspace again"
				>
					<Icon name="refresh" />
					{scanning ? "Scanning…" : "Refresh"}
				</button>
			</header>
			<form className="open-path" onSubmit={openPath}>
				<label className="field">
					<span className="field-label">Open by path</span>
					<input
						placeholder="/abs/path/to/name.board"
						value={path}
						onChange={(e) => {
							setPath(e.target.value)
							setPathError(undefined)
						}}
					/>
				</label>
				<button type="submit" disabled={!path.trim()}>
					Open
				</button>
			</form>
			{pathError && <p className="error">{pathError}</p>}
			{error && <p className="error">Cannot load boards: {error}</p>}
			{!shown && !error && <p className="muted empty-state">Loading…</p>}
			{shown?.groups.map((group) => (
				<CatalogGroup key={group.checkout.path} group={group} />
			))}
			{shown && shown.outside.length > 0 && (
				<section className="group">
					<header className="group-head">
						<h2>Outside known checkouts</h2>
					</header>
					<BoardList boards={shown.outside} />
				</section>
			)}
			{shown && shown.groups.length === 0 && shown.outside.length === 0 && (
				<p className="muted empty-state">
					{query.trim() ? "No boards match the search." : "No boards found."}
				</p>
			)}
			<footer className="home-footer">
				<p className="hint">
					The workspace scan follows git-tracked files; outside git it goes 4
					levels deep and skips hidden directories, node_modules and vendor.
				</p>
				<PeerHint />
			</footer>
		</main>
	)
}
