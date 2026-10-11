# Board API

The exact contract between the board core (`internal/board`), the HTTP server (`internal/boardhttp`), the CLI (`peer board ...`) and the SPA (`web/`). The design, semantics and invariants are in [BOARD.md](BOARD.md); this document fixes the wire shapes.

All JSON uses camelCase keys. Times are RFC 3339 UTC strings.

## Board file

`board.json`, `formatVersion: 1`:

```ts
type Board = {
	formatVersion: 1
	id: string // "b_" + random
	title: string
	roomId?: string // room that created the board
	revision: number // starts at 0 on create
	nodes: Node[]
	edges: Edge[]
	threads: Thread[]
	deleted: { id: string; rev: number }[]
	nextId: number // next numeric suffix for node, edge, thread and message ids
}

type Geometry = { x?: number; y?: number; w?: number; h?: number; z?: number }

type Node = Geometry & {
	id: string // "n12", "f3" for frames
	rev: number
	parentId?: string
} & (
		| { type: "card"; text: string; color?: CardColor }
		| { type: "image"; asset: string; caption?: string } // asset: "assets/<sha256>.<ext>"
		| { type: "frame"; title: string; order: number; preset?: "slide" }
	)

type CardColor = "gray" | "yellow" | "green" | "blue" | "purple" | "red"

type Edge = { id: string; rev: number; from: string; to: string; label?: string } // id "e7"

type Target = { kind: "board" } | { kind: "frame" | "node" | "edge"; id: string }

type Thread = {
	id: string // "t4"
	rev: number
	target: Target
	resolved: boolean
	orphaned: boolean
	targetLabel?: string
	messages: Message[]
}

type Author = { role: string; roomId?: string } // a human is { role: "user" }

type Message = { id: string; author: Author; body: string; createdAt: string } // id "m9"
```

Ids come from one counter: the prefix names the kind (`f` frame, `n` other node, `e` edge, `t` thread, `m` message), the number is `nextId`.

Serialization: tabs for indentation, arrays in stored order, a final newline, no HTML escaping. Node keys go in this order: `id`, `type`, `rev`, `parentId`, `text`, `color`, `asset`, `caption`, `title`, `order`, `preset`, `x`, `y`, `w`, `h`, `z`. A board directory also holds `.gitignore` with `.lock`.

## Packs

```ts
type Pack = { base: number; ops: Op[] } | { expect: Record<string, number>; ops: Op[] }

type Op =
	| { op: "add"; ref?: string; node: NewNode } // ref: "$name"
	| { op: "add"; ref?: string; edge: { from: string; to: string; label?: string } }
	| { op: "update"; id: string; set: Record<string, unknown> }
	| { op: "delete"; id: string; cascade?: boolean }
	| { op: "restore"; node?: Node; edge?: Edge; thread?: Thread } // exactly one; browser only
	| { op: "comment"; ref?: string; target: Target; body: string }
	| { op: "reply"; thread: string; body: string }
	| { op: "resolve"; thread: string; resolved: boolean }
```

- `NewNode` is a `Node` without `id` and `rev`.
- Any id field in an op may be a `$ref` defined by an earlier `add` or `comment` of the same pack.
- `update.set` may change only these fields; setting an optional field to `null` removes it:

  | Entity | Fields |
  | --- | --- |
  | card | `text`, `color`, `parentId` |
  | image | `caption`, `parentId` |
  | frame | `title`, `order`, `parentId` |
  | edge | `from`, `to`, `label` |
  | any node, browser only | `x`, `y`, `w`, `h`, `z`, `preset` (frame) |

  Threads change only through `comment`, `reply`, `resolve`, `delete` and `restore`. Messages never change.
- `delete` takes a node, edge or thread id. Undo of `comment` is `delete` of its thread; redo is `restore`.
- Undo covers every browser pack except `reply`: a posted reply has no inverse operation, so the undo history stops at it — older steps stay undoable, the reply itself is skipped.
- Geometry fields are `x`, `y`, `w`, `h`, `z` and `preset`. A CLI pack that sets any of them, or that contains `restore`, is rejected with `forbidden`.
- The CLI author comes from `--as` / `--room`. The browser author is always `{ role: "user" }`.

## Apply result

```ts
type ApplyResult = {
	revision: number
	refs: Record<string, string> // "$card" -> "n13"
	created: string[]
	changed: string[] // includes implicitly changed entities
	deleted: string[]
	warnings: Warning[]
}

type Warning = { kind: "roomLink"; message: string }
```

Every id in `created` and `changed` has `rev == revision` after the pack.

The HTTP apply response also carries the new snapshot: `ApplyResult & { board: Board; hash: string }`.

## Errors

CLI: the error JSON goes to stderr and the exit code is 1. HTTP: the same JSON with the status below.

```ts
type BoardError =
	| { error: "conflict"; base?: number; conflicts: { id: string; rev: number | null }[] } // 409; rev: the live rev, the tombstone rev for a deleted entity, or null for an unknown id
	| { error: "invalid"; problems: { path: string; message: string }[] } // 400: pack or board.json breaks the schema or an invariant; 500 with this kind: an I/O failure of the server
	| { error: "forbidden"; message: string } // 400 CLI geometry/restore; 403 Host, Origin, X-Peer
	| { error: "notFound"; message: string } // 404: no board at the key or path
	| { error: "unsupportedVersion"; formatVersion: number } // 400
	| { error: "tooLarge"; limit: number } // 413
	| { error: "unsupportedMedia"; message: string } // 415
	| { error: "room"; message: string } // 400: --room inactive or --as is not its member
```

## CLI output

`peer board create` and `peer board list` print `BoardRef` values (one object, or `{ "boards": BoardRef[] }`):

```ts
type BoardRef = {
	key: string // base64url, no padding, of the absolute canonical board path
	path: string
	url: string // "http://localhost:<port>/b/<key>"
	id?: string
	title?: string
	revision?: number
	rooms?: string[] // room ids of this checkout that link the board
	mtime?: string // of board.json
	error?: BoardError // the board cannot be read
	warnings?: Warning[] // create: roomLink
}
```

`peer board inspect` prints the semantic projection — the board without geometry:

```ts
type Inspect = {
	board: { key: string; path: string; url: string; id: string; title: string; roomId?: string; revision: number }
	nodes: SemanticNode[] // card: text, color; image: asset as an absolute path, caption; frame: title, order
	edges: Edge[]
	threads: Thread[]
	warnings?: Warning[] // roomLink when --room is given
}
```

`SemanticNode` is `Node` without `x`, `y`, `w`, `h`, `z`, `preset`. `--frame ID` keeps the frame, its descendants, the edges that touch them and the threads on any of them. `--ids` keeps the listed entities and the threads on them. `--overview` prints instead:

```ts
type Overview = {
	board: Inspect["board"]
	frames: { id: string; title: string; order: number; parentId?: string; nodes: number; openThreads: number }[] // by order
	loose: { id: string; type: "card" | "image"; label: string }[] // nodes outside frames; label = first line of text or caption
	boardThreads: { id: string; resolved: boolean; messages: number }[]
}
```

`--format mermaid` prints the whole board as a Mermaid flowchart instead of JSON; with `--overview`, `--frame` or `--ids` it fails with `invalid`, because the outside ends of a part's edges would show as cards. Errors stay JSON on stderr.

```
%% {"key":"…","path":"…","url":"…","id":"…","title":"…","revision":12}
flowchart TB
	subgraph f1["Plan"]
		n2["Parse the input"]
		n4[["chart"]]
	end
	n3["Validate it"]
	%% e5
	n2 -->|"then"| n3
	%% thread t6 on n2: open, 2 messages
```

- The first line is the `Inspect["board"]` header as one line of JSON, with `warnings` when `--room` gives one.
- A frame is a `subgraph` keyed by its id, nested as the frames nest. A card is `id["text"]`; an image is `id[["caption"]]`, or `"image"` without a caption.
- Each edge follows a `%% eN` line with its id. A thread is a `%%` line with its id, target, state and message count.
- Labels are plain text: `#`, `"`, `&`, `<`, `>`, `|`, `` ` ``, `*`, `[`, `]`, `\` and `_` outside a word become Mermaid entity codes, and so does a character that starts a markdown block or `%%` at the start of a line. A line break stays a real one.
- Card colors, frame `order`, image paths, `rev` and thread messages are only in the JSON.

`peer board apply` and `peer board import-image` print `ApplyResult`. `peer board schema` prints `{ "board": <JSON Schema>, "cliPack": <JSON Schema>, "browserPack": <JSON Schema> }`.

## HTTP

Base: `http://localhost:4747` (`PEER_PORT` overrides the port). The server listens on `127.0.0.1` and `[::1]`. Every request must have `Host: localhost:<port>` or `Host: 127.0.0.1:<port>`. Every `POST` must have `X-Peer: 1` and `Origin` equal to `http://` + the request's `Host`; API and SSE requests with any other `Origin` are rejected. Links use `localhost`: Claude Code makes only `https:` and `http://localhost` links clickable. Bodies are `application/json` except asset upload.

| Method and path | Body | Response |
| --- | --- | --- |
| `GET /` | | SPA `index.html` |
| `GET /b/{key}` | | SPA `index.html` if the key decodes to a `*.board` directory with a valid `board.json`; otherwise 404 with a plain HTML error |
| `GET /_app/*` | | SPA static files (Vite `base: "/_app/"`) |
| `GET /api/health` | | `{ ok: true, peerHome: string, port: number, version: string }` |
| `GET /api/catalog` | | `Catalog` |
| `POST /api/workspaces` | `{ path: string }` | `{ ok: true }`; the server scans it |
| `POST /api/rescan` | `{}` | `Catalog` after a new scan of all workspaces |
| `GET /api/boards/{key}` | | `{ board: Board; hash: string; ref: BoardRef }` |
| `POST /api/boards/{key}/apply` | `Pack` | `ApplyResult & { board: Board; hash: string }` |
| `POST /api/boards/{key}/assets` | image bytes, `Content-Type: image/png\|jpeg\|gif\|webp` | `{ asset: string; width: number; height: number }` |
| `GET /api/boards/{key}/events` | | SSE |
| `GET /b/{key}/assets/{file}` | | the image file |

`hash` is the hex SHA-256 of `board.json` bytes.

```ts
type Catalog = {
	workspaces: string[]
	groups: {
		checkout: { path: string; name: string } // name: base name of the checkout path
		rooms: { id: string; active: boolean; startedAt: string; endedAt?: string; boards: BoardRef[] }[] // newest first
		store: BoardRef[] // $PEER_HOME/repos/<checkout>/boards/*.board, newest mtime first, minus boards shown in rooms
		found: BoardRef[] // *.board under workspaces inside this checkout, minus rooms and store
	}[]
	outside: BoardRef[] // found in workspaces outside any known checkout
}
```

### SSE

`GET /api/boards/{key}/events` sends `event: board` with data `{ revision: number; hash: string; external: boolean }`:
- once on connect;
- whenever the 500 ms poll sees a new `hash`.

`external` is true when the hash changed and the revision did not grow. On any event with a hash the tab does not know, it reloads the snapshot. The tab clears its undo history when `external` is true, and also when the reloaded snapshot has a different hash at a revision not greater than the last one the tab knew: after a reconnect or a server takeover the server has no earlier poll to compare with. A comment line `: ping` goes out every 15 s.

### Headers

- HTML: `Content-Security-Policy: default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'`, `X-Frame-Options: DENY`.
- JSON: `X-Content-Type-Options: nosniff`, `Cache-Control: no-store`.
- Assets: `Cross-Origin-Resource-Policy: same-origin`, `X-Content-Type-Options: nosniff`.
- No CORS headers on any response. API and SSE reject a request with an `Origin` other than the server's own.

## Room link

`session.json` of a room gets `boards: string[]` — absolute canonical board paths, no duplicates, in first-use order.

## Server files

- `$PEER_HOME/server.lock` — held by the TUI that runs the server.
- The SPA embed lives in the Go package `web` (`web/embed.go`, `//go:embed all:dist`). `web/dist/.keep` keeps `go build` working before the SPA is built. When `web/dist/index.html` is missing, `GET /` answers 503 with a hint to run `make web`.
