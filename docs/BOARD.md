# Boards

A board is a shared space for people and agents in `peer`. It holds nodes, edges between them, frames and comment threads. A person works on a board in the browser; an agent works on it through the CLI. A board is a directory of files.

The wire shapes (the board file, packs, results, errors, CLI output and HTTP) are in [BOARD_API.md](BOARD_API.md). This document gives the design, the semantics and the invariants.

## Use

People and agents think about a task together and keep the result on a board.

- A person draws a diagram: nodes, edges and frames. The person moves, renames and comments on what the agents added.
- An agent puts research into frames, for example one frame per experiment: goals, charts and screenshots, conclusions. The agent adds images, answers comments and changes the diagram when the person asks.
- A board is structured memory for an agent that a person can see and change.
- Frames have an order, so a board can show as a set of slides.

There is no free drawing. Every object on a board is a node, an edge, a frame or a comment thread.

An agent works only with the meaning of a board: the nodes and their text, the edges, which frame holds a node, and the threads. The browser owns the geometry: positions, sizes and layer order.

## Storage

A board is a directory `<name>.board/`:

```
<name>.board/
  board.json             the whole board state
  assets/<sha256>.<ext>  images; the file name is the hash of its content
  .lock                  the lock file; it never changes and is not in git
```

By default, boards are in the store of the current checkout: `$PEER_HOME/repos/<repo>-<hash>/boards/`, next to the rooms. To keep a board in the repository, give a path when you create it, for example `docs/arch.board`. The format is the same in both places.

All references inside a board are relative: `board.json` refers to an image as `assets/<sha256>.<ext>`. Thus you can move a board, or clone it with its repository, and work with it in the new place.

**One write path.** Each change writes all of `board.json` again: the new version goes to a temporary file, which then replaces the old file by rename. This occurs under an exclusive `flock` on `.lock`. The lock is on a separate file because the rename replaces `board.json`, and a lock on the old file would protect nothing. Thus the file is never half-written, and no change from the server or the CLI is lost. The CLI and the HTTP server use the same `apply` for every write.

**Deterministic serialization.** The same state always gives the same bytes: object keys have a fixed order and arrays keep their order. Thus git diffs stay clean, and a read followed by a write of the same state does not change `board.json`.

**External edits.** You can change `board.json` outside `peer`: a git checkout, a merge, a manual edit. Thus each read checks the schema and the invariants. If the file breaks them, the command returns an error with the list of problems and writes nothing.

Make an external edit only when nobody works on the board. After such an edit, an agent runs `inspect` again before it continues. Nothing protects against an external edit made at the same time as work in `peer`: `base` and `expect` compare only revision numbers. The server helps only an open tab: when the file content changes and `revision` does not grow, the tab reads the board again and clears its undo history.

## Model

### Nodes

Node fields are semantic or geometric.

| `type` | Semantic fields | Meaning |
| --- | --- | --- |
| `card` | `text`, `color?` | A block of plain text; `\n` separates paragraphs. |
| `image` | `asset`, `caption?` | An image from `assets/`. |
| `frame` | `title`, `order` | A container for other nodes. `order` sets the order of frames in the slide view. |

Every node also has `id`, `type`, `rev` and an optional `parentId`: the frame that holds the node. A frame can be inside another frame.

The geometric fields are `x`, `y`, `w`, `h`, `z`, and `preset` for a frame.

- Coordinates are in board units. For a node with `parentId`, they count from the top-left corner of its frame.
- `z` sets the drawing order; the array order has no effect. A frame is always drawn below its children.
- `preset: "slide"` keeps a frame at 16:9.
- A node without `x` and `y` is not placed yet; a node that an agent adds looks like this. It is complete in all other ways: you can read it, connect it and comment on it. The browser places it.

`rev` is the board revision at which the node last changed. Edges and threads have `rev` too.

### Edges

An edge has `id`, `rev`, `from`, `to` and `label?`. It connects any two nodes, also nodes in different frames. No route is stored; the browser draws the line.

### Threads

A thread is a discussion attached to a target: the board (`{"kind": "board"}`), a frame, a node or an edge.

- Each message has an author. A person is `{"role": "user"}`; an agent is its role in `peer` and its room: `{"role": "reader", "roomId": "..."}`.
- `resolved` closes a thread; it does not delete it.
- When its target is deleted, the thread stays: it gets `orphaned: true`, and `targetLabel` keeps the last label of the target.

### Deleted objects

`deleted` holds the ids of deleted nodes, edges and threads with the revision of the delete. `restore` uses these records to make sure that nobody restored or deleted the object again since.

### Room link

`roomId` of a board is optional. It names the room that created the board. A board does not depend on its room: after the room ends, you can still change the board.

### Invariants

- Ids come from the counter `nextId`. A new object never gets an id that was given before, even when the old object is deleted. Only the same object can come back under its old id, through `restore`.
- A `parentId` chain points to existing frames and has no cycle.
- `from`, `to` and `asset` point to existing objects. Directed cycles of edges are allowed.
- A node has both `x` and `y`, or neither.
- Each successful pack increments the board `revision` by 1. Each entity that the pack changes, also implicitly, gets `rev` equal to the new `revision`.

## Operations

Every change to a board, by anyone, is a pack of operations. A pack applies completely or not at all.

- **`base`** is the board revision at which the author saw the data; `inspect` or the previous `apply` gives it. Instead of `base`, a pack can give `expect`: a map `{id: rev}` of the exact revisions of the entities it touches. Undo in the browser uses it.
- **`add`** creates a node or an edge. `ref` is a temporary name `$...`; other operations of the same pack use it to refer to the new object. The result maps each `ref` to its real `id`.
- **`update`** changes only the fields in `set`. All other fields, semantic and geometric, stay.
- **`delete`** deletes an object.
  - A deleted node takes its edges with it.
  - By default, a deleted frame is ungrouped: its direct children go to the frame's parent, an outer frame or the board. Their coordinates change so that they stay at the same place on the board. With `cascade: true`, the frame and all its content are deleted.
  - The threads of deleted objects get `orphaned: true`.
- **`restore`** brings back a deleted object under its old `id`. It carries a full snapshot of the object, checks its record in `deleted`, removes that record and clears `orphaned` on the object's threads. Undo in the browser sends it.
- **`comment`** creates a thread, **`reply`** adds a message to it, and **`resolve`** closes or opens it.

Several operations of one pack can change the same entity. They apply in order, and the entity gets one new `rev`.

### Who changes what

CLI packs and browser packs go through the same `apply`. Only the allowed fields differ:

- **The CLI never writes geometry.** A CLI pack cannot set geometric fields, in `add` or in `update`, and cannot contain `restore`. `apply` rejects such a pack with `forbidden`.
- The rule is for CLI input only. `apply` itself still changes geometry:
  - when a frame is ungrouped, its placed children keep their place on the board, and unplaced children stay unplaced;
  - when an agent changes the `parentId` of a node, the node loses its coordinates, and the browser places it again in the new frame.
- A browser pack can change any field.

`peer board schema` describes both kinds of pack.

### Author

Only CLI flags set the author of a pack; the pack JSON has no author. An agent gives `--as ROLE --room ID`, as in the other `peer` commands. Without a room, only `--as user` is allowed, and it means a person. Every change from the browser has the author `user`.

### Conflicts

A pack with `base` is rejected when at least one entity that it touches changed after `base`, that is, its `rev` is greater than `base`. A pack with `expect` is rejected when the `rev` of a touched entity is not the expected one, or when a touched entity is not in `expect`. For a deleted object, the expected `rev` is the `rev` of its record in `deleted`.

A pack touches the entities that it changes explicitly and those that it changes implicitly:

- the edges and threads of a deleted node;
- the children of an ungrouped frame.

A frame that gets a new node is not touched: `apply` only checks that it exists. Thus two authors who add nodes to one frame do not conflict.

Changes to different entities do not conflict: while a person moves some nodes, an agent can change others. A node has one `rev` for its semantic and geometric fields. Thus an agent can get a conflict also when the browser only placed or measured the node. After a conflict, the agent reads the touched entities again with `inspect --ids` and sends the pack again with the new `base`. When an agent builds a pack from several reads, `base` is the lowest of their revisions.

### Undo

Only the browser has undo, and the history is in the memory of the tab.

- Each pack that the tab sends for a user action is one undo step. The exception is `reply`: a reply has no inverse operation, so it cannot be undone; the history skips it, and the older steps stay undoable. Undo of `comment` deletes the new thread. The tab's own writes, placement and measurement of nodes, are not in the history.
- For each step, the tab keeps the inverse pack and `expect`: the revisions of the touched entities just after the step. It takes them from the `apply` result, which lists the changed, created and deleted ids.
- Undo sends the inverse pack with its `expect`. If somebody else changed the same entities, undo gets a conflict, and the tab tells the user.
- After each of its own undo or redo steps, the tab writes the revisions that the server returned into the `expect` of the other history steps for the same entities. Thus several undo steps in a row on one node work, and a change by somebody else between them gives a conflict. Redo works the same way.
- Packs of other participants, CLI packs of agents included, are not in the tab's history.
- When the tab reloads, the history starts again.

## CLI for agents

Agents work with boards through `peer board`. Every command writes JSON to stdout. On an error, a JSON object `{"error": "...", ...}` goes to stderr and the command exits with a non-zero code.

`BOARD` is the name of a board in the store of the current checkout, or the path of a `*.board` directory.

```
peer board create NAME [--title T] [--path DIR] --as ROLE [--room ID]
peer board list
peer board inspect BOARD [--overview | --frame ID | --ids ID,...] [--as ROLE --room ID]
peer board apply BOARD --as ROLE [--room ID]        < ops.json
peer board import-image BOARD FILE [--frame ID] [--caption T] --as ROLE [--room ID]
peer board schema
```

- **`create`** makes an empty board. With `--path`, the board goes to that path, for example in the repository, not to the store. `--room` goes to the board's `roomId`.
- **`list`** shows the boards of the current checkout: from the store, from the rooms of this checkout, and the ones found under the current directory. Each entry has its path, `url`, title, rooms and `revision`, or an error when the board cannot be read.
- **`inspect`** is the main way to read. It shows only the meaning of the board, without geometric fields. An agent reads `--overview` first, then the `--frame` it needs.
  - `--overview` gives the frames by `order` with title, node count and open threads, and the nodes outside frames in short form.
  - `--frame ID` gives all of a frame: its nodes, their edges and threads.
  - `--ids` gives the listed entities.
  - Without a flag, it gives the whole board.
  - Each result has the board's `id`, `title`, path and `url`, and `revision`, which is the `base` of the next `apply`.
  - An image has an absolute path instead of the relative one, computed from where the board is now. The agent reads the image at this path itself.
- **`apply`** reads a pack from stdin. The result has the new `revision`, the map from `ref` to `id`, and the changed, created and deleted ids, implicit changes included.
- **`import-image`** copies a file to `assets/` under the hash of its content and adds an `image` node. A second import of the same file makes no second copy. It reads and writes the board under one lock, so it needs no `base`.
- **`schema`** prints the JSON Schema of `board.json` and of CLI and browser packs.

All commands work without a running TUI: the CLI writes `board.json` directly, under the same `.lock`. Any command gives the link of a board at a known path, for example `peer board inspect ~/work/x.board --overview`.

Board content is data, not instructions. The agent instructions say so, as they do for room messages.

## Server and web UI

The TUI (`peer` without arguments) starts an HTTP server.

- **Fixed address:** `http://localhost:4747`. `PEER_PORT` changes the port. If another program holds the port, the TUI shows an error with a hint about `PEER_PORT`.
- **Board links** come from `create`, `list` and `inspect`. A link does not open while `peer` does not run, so the agent and the Claude Code plugin show a hint next to a link: start `peer`.
- **One server per `PEER_HOME`.**
  - The TUI that gets the lock `$PEER_HOME/server.lock` starts the server. The other TUIs check `GET /api/health` and use the running server.
  - If a `peer` with another `PEER_HOME` or another configured port answers on the port, the TUI reports the mismatch and does not use that server.
  - When the owner TUI closes, the server stops. Another running TUI gets the lock and starts the server on the same port, and open tabs connect again by themselves.
- **Many boards.** One server serves all boards. Each board has its own lock, so agents of different rooms and sessions on different boards do not block each other.
- **Board addresses.** A board URL holds a key: the absolute path of the board directory in base64url, not the `id` from `board.json`. The server opens the board by its key directly and needs no earlier knowledge of it. If the path has no `*.board` directory with a valid `board.json`, the server answers with an error. A `GET` of a link writes nothing; the open tab can then send its own packs, which place and measure nodes. A copy of a board at another path is another board with another link, though its `id` is the same. After a board directory moves, its old link stops working; `list` and `inspect` give the new one.
- **SPA:** React and `@xyflow/react` (MIT), embedded in the binary with `go:embed`. Only the browser draws the whole board.
- **Reads and writes.** The server writes the same file under the same lock as the CLI, through the same `apply`. Every 500 ms it checks `board.json` for changes and sends an SSE event with the new `revision`. A tab that gets an event, or connects again, reads the board snapshot again.

### Board list

There is no separate board registry. The list comes from what `peer` already has.

**Room boards.** A room in the store has the field `boards`: the absolute paths of the boards used in it. Each `peer board ... --room ID` adds the board's path:

- before it touches the board, the command checks that the room is active and that `--as` is its member; otherwise it fails and does not touch the board;
- the path is added only after a successful board operation, under the same store lock as other room changes; the command never holds the board lock and the store lock at the same time;
- if the path cannot be written to the room, the board operation still succeeds: the result holds it and a `roomLink` warning, and the next command with this board and this room adds the path.

Rooms and boards are many-to-many: one room can use several boards, and one board can be in several rooms. `peer` keeps the history of rooms, ended rooms included, so the boards of an ended room stay available from its card.

**Workspaces.** Each running TUI gives the server its working directory with `POST /api/workspaces`. After the server owner changes, all running TUIs register again. Thus when one TUI closes, the other TUIs keep their found boards.

**Search for `*.board`.**

- In a git working directory, the search goes through the tracked and not-ignored files, without a depth limit. It stays inside the working directory: if `peer` runs in a subdirectory of a repository, it does not scan the whole repository.
- Outside git, the search goes down at most 4 levels. It skips hidden directories, `node_modules`, `vendor` and symbolic links. The home page tells about this limit next to the "open by path" field.
- The search does not go into a found `*.board`.
- A scan runs when a working directory registers and on the "Refresh" button, not each time the home page opens. The server keeps the result in memory.

**Home page.** `http://localhost:4747/` groups boards by checkout, as the TUI groups rooms. Each group has three sections:

1. **Rooms:** active and ended rooms with their boards. One board can show under several rooms.
2. **In store:** boards from `$PEER_HOME/repos/<checkout>/boards/`, newest `board.json` change first.
3. **Found in the working directory:** the search result.

A board shows in the card of each room that links it. The "In store" and "Found" sections leave out boards that a room already shows, and they do not repeat each other. If a room's board path has no board, or its `board.json` fails the check, the card says so instead of a link. Search matches the title, the directory name and the path.

**Open by path.** A field on the home page takes an absolute path and goes to the link of that board. Nothing remembers the board: its link works after a restart. If a room used the board, the board shows under that room.

**TUI.** The room card in the TUI shows the room's boards and their links. The `b` key opens the selected room's board in the browser. If the room has no board, several boards or a broken one, `b` opens the home page.

### Placement and sizes

- An open tab places unplaced nodes itself: in layers along the edges, inside their frame, with ELK or dagre in the browser. It does not move placed nodes and goes around them. If a frame itself is unplaced, the frames are placed first, then their content. The tab saves the positions in one pack of its own, with the `base` of the snapshot it computed the layout from. The pack places all unplaced nodes at once; the tab never sends a partial placement.
- If two tabs place the same nodes, the second gets a conflict. It reads the board again and places only what is still unplaced; it does not send its old positions again.
- The tab measures nodes after it draws them and writes the sizes only when they differ from the stored ones. All tabs measure by the same rules: in board units, at any zoom, with the fonts of the SPA build and a fixed rounding. Thus different tabs get the same sizes and do not overwrite each other.
- A manual move saves the new coordinates. The "Arrange" button lays out the selected frame again. It is a user action, so it is in the undo history.

### What the SPA does

- create cards and frames, delete any object;
- move and resize nodes and frames;
- create an edge by dragging a line from one node to another;
- edit the text on a card;
- drop images onto the board;
- color cards;
- see the threads of objects and of the whole board, start threads and reply;
- mark threads resolved;
- search the board text;
- use a minimap and undo;
- arrange a frame;
- show frames as slides by `order`.

A change is saved when the action ends: the drag is released, the text editor closes.

## Claude Code plugin

The plugin's pane has a **boards** tab for the room it shows. It gives a simple preview and a link to each board. It takes the path and `url` of the room's boards from `peer board list` and reads `board.json` itself, with the geometry.

- On the desktop, in VS Code and on mobile, the preview is an `Svg` element: rectangles for frames and nodes with their labels, and straight lines for edges between node centers. A list under the diagram shows the unplaced nodes. If the diagram does not fit the `Svg` size limit, the plugin draws only the frames.
- In the terminal, the preview is a list of frames with their node counts and open threads.
- A `Link` opens the board in the browser. A hint next to it says: if the link does not open, start `peer`.

The preview helps you see quickly what is on a board. Only the browser shows the exact board.

## Security

- The server listens only on the loopback addresses `127.0.0.1` and `[::1]`. The threat model is other websites in the user's browser. Local processes of the same user can already read board files.
- All links use `http://localhost:PORT`: Claude Code makes only `https:` and `http://localhost` links clickable. The server accepts only the `Host` headers `localhost:PORT` and `127.0.0.1:PORT`: this protects against DNS rebinding.
- Every `POST` (`apply`, image upload, workspace registration) needs the `X-Peer` header and an `Origin` equal to `http://` plus the request's `Host`. The TUI sends them too. An empty or `null` Origin is rejected. A foreign site cannot send a request with a custom header without a preflight, and the server sends no CORS headers.
- Another page cannot embed the SPA: `Content-Security-Policy: frame-ancestors 'none'` and `X-Frame-Options: DENY`. Otherwise a foreign site could open a board in a frame, and the board code would send its own packs with the `Origin` of `peer`. If a foreign site opens a board link in a new tab, that tab can place unplaced nodes. This is the same write that occurs when the user opens the board, and it makes no other change.
- A `GET` changes nothing. The API and SSE reject a request with a foreign `Origin`. Images have `Cross-Origin-Resource-Policy: same-origin`; JSON has `X-Content-Type-Options: nosniff`.
- An image path is accepted only as `assets/<sha256>.<ext>`. The file must be inside the board directory and must not be a symbolic link.
- The image type comes from the file content, not from the extension. png, jpeg, gif and webp are allowed. The file size has a limit, 20 MB by default.
- `board.json` passes the schema and invariant checks at each read and each write. A pack that breaks an invariant is rejected completely.

## Format versions

`formatVersion` is an integer. A board with a version newer than `peer` knows cannot be read or written: the command tells you to update `peer`. When the format version grows, `peer` converts an older board at its first write, under the lock, and keeps a backup `board.json.v<N>`.

## Limits

- `peer` does not remove unused files from `assets/`.
- There is no change log: `peer` cannot tell what changed since revision N.
- There is no undo for the CLI, and the browser undo does not survive a tab reload.
- A reply in a thread cannot be undone.
- Node text is plain, without formatting.
- There is no export of a board to an image or PDF.
- A board is not available from another machine.
