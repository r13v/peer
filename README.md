# peer

Local CLI for any two coding agents working in one Git checkout. One writes; the other discusses and reviews. Messages and the transcript stay on this Mac. No MCP server or model API is needed.

## Install

Requires macOS, Git, and two local agent chats that can run shell commands. Install the current `latest` release with one command:

```sh
curl -fsSL https://github.com/r13v/peer/releases/download/latest/install.sh | sh
```

The installer checks the archive's SHA-256 digest and puts `peer` in `~/.local/bin`. Add `~/.local/bin` to `PATH` if needed. To get the newest CLI later, run `peer update`. Every push to `main` replaces the single `latest` release.

Install the skill separately for the agents you use with Node.js and `npx skills`. For Claude Code, Codex, and GitHub Copilot:

```sh
npx skills add r13v/peer --skill peer -g -a claude-code -a codex -a github-copilot -y
```

This command installs only the skill; `peer` must also be on `PATH`. Restart the apps after installation. `peer update` updates only the CLI; update the skill with `npx skills update peer -g`.

To build the CLI from source instead, use Go 1.22 or newer: create `~/.local/bin` and run `go build -o "$HOME/.local/bin/peer" .`.

## Pair on a task

Open one **local** chat in Claude Code Desktop's Code tab in the Git checkout, without a separate worktree, and send your task:

```text
/peer add CSV export to the reports page
```

Claude becomes the writer and starts the session with `peer start --writer claude --reader codex`, which runs Codex headless (`codex exec`) in the same checkout. Ask for a headed reader (`peer start --headed`) to open a new Codex Desktop chat instead, with the reader prompt filled in; press Enter there, since neither app sends a deep-linked prompt by itself. The agents then discuss the approach through `peer send` and `peer wait`, Claude implements it, Codex reviews the diff, and Claude ends the session. To watch their conversation, run `peer follow` in a terminal in the checkout, or run `peer` to pick a session.

To start from Codex instead, send `$peer <task>` in a local Codex chat. Codex becomes the writer and runs Claude Code headless (`claude -p`) as the reader, or opens a Claude Code Desktop chat with `--headed`. The agent you send the task to is always the writer.

To pair with GitHub Copilot, open a Copilot app session in the **local repository** (or use Copilot CLI or VS Code agent mode) and send Claude `/peer pair with Copilot: add CSV export`. Claude starts with `peer start --writer claude --reader copilot` and gives you the prompt to send Copilot. If Copilot's local sandbox blocks writes to `~/.peer/`, allow that directory in its sandbox policy or turn off the local sandbox for this session.

Both agents must use the same checkout and be able to run `peer`.

## CLI

Run `peer` with no arguments in a terminal to pick a session: it lists active sessions in every checkout, then the 10 latest ended sessions of the current checkout. Use ↑/↓ (or j/k) to select and Enter to open. An active session streams live, and an ended one shows its transcript and summary. In a session with a headless reader, Tab switches between the transcript and the reader's log. Esc returns to the list, and q or Ctrl-C quits. The list works outside a checkout too; then it shows only active sessions. An opened transcript is printed on the normal screen, so it stays in the terminal's scrollback after you return to the list.

Other commands run from anywhere inside the shared Git checkout:

```sh
peer skills flow
peer skills writer
peer skills reader
peer update
peer status
peer start --writer codex --reader copilot
peer send --as codex <<'MESSAGE'
Please challenge this approach before I edit.
MESSAGE
peer wait --as copilot
peer follow
peer end --as codex
peer history
peer log SESSION_ID
peer log SESSION_ID > transcript.txt
```

The skill only runs `peer skills flow`, so the workflow always matches the installed CLI. If an agent reports that `peer` or `skills flow` is unknown, install or update the CLI. When the reader is `codex` or `claude`, `start` runs its CLI headless in the checkout: Codex runs in its `workspace-write` sandbox with the session store added, Claude gets only Read, Grep, Glob, Skill and Bash limited to `peer` and read-only `git` commands, with any other request denied instead of prompting, and both are told not to edit files. A headless reader is pinned to its session through `PEER_SESSION` and stops once that session ends. Its output and exit status go to `reader.log` in the session directory; in `peer`, press Tab in a session to switch between the transcript and that log. `start --headed` opens a new desktop chat with the prompt filled in instead, but only when the reader's app is already open (checked by bundle ID on macOS); a closed app is not launched and the reader runs headless. For other readers, the flow tells the writer to give you the prompt.

Participant names are distinct lowercase IDs starting with a letter and containing only `a-z`, `0-9`, `-`, or `_` (up to 64 characters). You can pair two sessions of the same app by naming them `codex-main` and `codex-review`. `wait` returns one JSON message and marks it delivered, or `{"status":"timeout"}` after 90 seconds, below Claude Code's two-minute Bash limit. A completed chat cannot be sent to; end it before starting another task in the checkout.

The CLI stores private session files and an append-only `messages.jsonl` transcript under `~/.peer/repos/CHECKOUT-HASH/sessions/SESSION_ID/`, for example `~/.peer/repos/peer-chat-42bda30d37ea509a/sessions/20260929-120911/`. The checkout name is lowercased and the hash keeps checkouts with the same name apart. A session ID is its start time in local time (`YYYYMMDD-HHMMSS`, with `-2` appended if another session started in the same second); `started_at` in `session.json` stays in UTC. Set `PEER_HOME` to use another local directory; both agents must use the same value.

`history` prints one line per session: ID, start time, writer→reader, message count, duration, and `active` or `ended`. `log ID` prints one transcript and exits. `follow` displays the dialogue live, prints a message count and duration when the writer ends the session, and exits. It waits for the next session if none is active or the active one has ended, so you can open it before `peer start`. In a terminal, transcripts color authors, times, `code`, **bold** and list markers, and turn paths to existing files into clickable links; set `NO_COLOR=1` to disable this. While following, a bottom line shows each participant as `waiting` (it called `peer wait` in the last 2 seconds) or `busy` with the time since it last waited; this is inferred from polling, not a report of actual work. On macOS, following also sends a notification when a session you watched ends or when no message arrives for 10 minutes. Links open `file://PATH` by default; set `PEER_EDITOR_URL` to jump to the line in an editor, for example `vscode://file/{path}:{line}`. The transcript contains messages sent through this CLI, not the agents' private reasoning or tool output. You can give a saved session ID to an agent later and ask it to review the conversation and suggest specific changes to the workflow or skills.

This CLI does not inject prompts into idle desktop chats. Each participating agent must keep calling `wait` while a reply is needed. The reviewer role is an instruction and session record, not an operating-system restriction on file writes; use the app's read-only or Plan permission mode if that guarantee matters.
