# peer

Local CLI for any two coding agents working in one Git checkout. One writes; the other discusses and reviews. Messages and the transcript stay on this Mac. No MCP server or model API is needed.

## Install

Requires macOS, Git, Homebrew, and two local agent chats that can run shell commands. Install the CLI from the [r13v/apps](https://github.com/r13v/homebrew-apps) tap:

```sh
brew install --cask r13v/apps/peer
```

Every push to `main` publishes a new patch version; get it with `brew upgrade --cask peer`, and check the installed one with `peer --version`. If you installed `peer` earlier with the `curl` installer, remove that copy so it does not shadow the Homebrew one: `rm ~/.local/bin/peer`.

Install the skill separately for the agents you use with Node.js and `npx skills`. For Claude Code, Codex, and GitHub Copilot:

```sh
npx skills add r13v/peer --skill peer -g -a claude-code -a codex -a github-copilot -y
```

This command installs only the skill; `peer` must also be on `PATH`. Restart the apps after installation. `brew upgrade` updates only the CLI; update the skill with `npx skills update peer -g`.

To build the CLI from source instead, use Go 1.27 or newer: create `~/.local/bin` and run `go build -o "$HOME/.local/bin/peer" .`. `make` formats, lints (with [golangci-lint](https://golangci-lint.run) v2), tests and builds `./peer`; `make test`, `make lint` and `make build` run one step.

## Pair on a task

Open one **local** chat in Claude Code Desktop's Code tab in the Git checkout, without a separate worktree, and send your task:

```text
/peer add CSV export to the reports page
```

Claude becomes the writer and starts a room named after the task with `peer start csv-export --writer claude --reader codex`, which runs Codex headless (`codex exec`) in the same checkout. Ask for a headed reader (`peer start --headed`) to open a new Codex Desktop chat instead, with the reader prompt filled in; press Enter there, since neither app sends a deep-linked prompt by itself. The agents then discuss the approach through `peer send` and `peer wait`, Claude implements it, Codex reviews the diff, and Claude ends the session. To watch their conversation, run `peer` in a terminal. Several rooms can be active in one checkout at once, each with its own writer and reader.

To start from Codex instead, send `$peer <task>` in a local Codex chat. Codex becomes the writer and runs Claude Code headless (`claude -p`) as the reader, or opens a Claude Code Desktop chat with `--headed`. The agent you send the task to is always the writer.

To pair with GitHub Copilot, open a Copilot app session in the **local repository** (or use Copilot CLI or VS Code agent mode) and send Claude `/peer pair with Copilot: add CSV export`. Claude starts with `peer start csv-export --writer claude --reader copilot` and gives you the prompt to send Copilot. If Copilot's local sandbox blocks writes to `~/.peer/`, allow that directory in its sandbox policy or turn off the local sandbox for this session.

Both agents must use the same checkout and be able to run `peer`.

## CLI

Run `peer` with no arguments in a terminal to watch rooms. The left pane lists every room in every checkout: active rooms first, then ended ones grouped by day (Today, Yesterday, then the date), each group newest first. A row shows a mark (● active, ○ ended, ✕ ended other than by `end`), the room name and its checkout, highlighted for the current one, and below them the start time of an ended room, the message count, the duration, the reason a room ended other than by `end`, and the participants. The right pane shows the selected room's transcript, live while the room is active and with a message count and duration once it ends; its title shows each participant as `waiting` (it called `peer wait` in the last 2 seconds) or `busy` with the time since it last waited, which is inferred from polling, not a report of actual work. The bottom pane shows the headless reader's log. Messages are rendered as Markdown with highlighted code; a pane that you scrolled up stays put while new lines arrive and shows `↓ new`. The list works outside a checkout too. On macOS, `peer` sends a notification when the room you are watching ends.

| Key | Action |
| --- | --- |
| j/k, ↑/↓ | select a room, or scroll the focused pane |
| Tab, Enter | move focus between the rooms, transcript and log; Enter opens the selected room |
| PgUp/PgDn, Ctrl-U/Ctrl-D, g/G, ←/→ | scroll by page, half page, to the top or bottom, or sideways |
| ] / [ | jump to the next or previous message |
| / then n/N | search the focused transcript or log, like in vim; a lowercase query ignores case, Esc clears it |
| l, s | hide or show the reader log or the room list |
| m, T | switch between Markdown and plain text, or cycle the Markdown theme |
| x | close the selected active room, for example after you stopped its writer |
| ?, q | show all keys, or quit (also Ctrl-C) |

The mouse selects rooms and panes, and the wheel scrolls the pane under the pointer.

Other commands run from anywhere inside the shared Git checkout:

```sh
peer skills flow
peer skills writer
peer skills reader
peer --version
peer start csv-export --writer codex --reader copilot
peer status
peer status csv-export
peer send csv-export --as codex <<'MESSAGE'
Please challenge this approach before I edit.
MESSAGE
peer wait csv-export --as copilot
peer end csv-export --as codex
peer history
peer log csv-export
peer log csv-export > transcript.txt
```

The skill only runs `peer skills flow`, so the workflow always matches the installed CLI. If an agent reports that `peer` or `skills flow` is unknown, install or update the CLI. When the reader is `codex` or `claude`, `start` runs its CLI headless in the checkout: Codex runs in its `workspace-write` sandbox with the session store added, Claude gets only Read, Grep, Glob, Skill and Bash limited to `peer` and read-only `git` commands, with any other request denied instead of prompting, and both are told not to edit files. A headless reader gets the room ID in its prompt and stops once that room ends. Its output and exit status go to `reader.log` in the session directory; when it exits for any reason, the room ends too, with a reason such as `codex exited with status 1`, so the writer's `wait` stops instead of waiting on a reader that is gone. A room otherwise ends only by `peer end` or x in `peer`: stopping or closing the writer's chat does not end it. `peer` shows that log below the transcript. `start --headed` opens a new desktop chat with the prompt filled in instead, but only when the reader's app is already open (checked by bundle ID on macOS); a closed app is not launched and the reader runs headless. For other readers, the flow tells the writer to give you the prompt.

Participant names are distinct lowercase IDs starting with a letter and containing only `a-z`, `0-9`, `-`, or `_` (up to 64 characters). You can pair two sessions of the same app by naming them `codex-main` and `codex-review`. `wait` returns one JSON message and marks it delivered, or `{"status":"timeout"}` after 90 seconds, below Claude Code's two-minute Bash limit. An ended room cannot be sent to; start a new room for the next task. `status` without an ID prints each active room in the checkout as one JSON line.

The CLI stores private session files and an append-only `messages.jsonl` transcript under `~/.peer/repos/CHECKOUT-HASH/sessions/SESSION_ID/`, for example `~/.peer/repos/peer-chat-42bda30d37ea509a/sessions/csv-export/`. The checkout name is lowercased and the hash keeps checkouts with the same name apart. A room ID is the name given to `start` (1–40 characters: `a-z`, `0-9` or `-`, starting with a letter), with `-2`, `-3` and so on appended if the checkout already had a room with that name; `started_at` in `session.json` is in UTC. Set `PEER_HOME` to use another local directory; both agents must use the same value.

`history` prints one line per session: ID, start time, writer→reader, message count, duration, and `active` or `ended`. `log ID` prints one transcript and exits. In a terminal, it colors authors, times, `code`, **bold** and list markers, and turns paths to existing files into clickable links; set `NO_COLOR=1` to disable this. Links open `file://PATH` by default; set `PEER_EDITOR_URL` to jump to the line in an editor, for example `vscode://file/{path}:{line}`. The transcript contains messages sent through this CLI, not the agents' private reasoning or tool output. You can give a saved session ID to an agent later and ask it to review the conversation and suggest specific changes to the workflow or skills.

This CLI does not inject prompts into idle desktop chats. Each participating agent must keep calling `wait` while a reply is needed. The reviewer role is an instruction and session record, not an operating-system restriction on file writes; use the app's read-only or Plan permission mode if that guarantee matters.
