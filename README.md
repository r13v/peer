# peer

Local CLI for a Claude Code Desktop and Codex Desktop pair working in one Git checkout. One agent writes; the other discusses and reviews. Messages and the transcript stay on this Mac. No MCP server or model API is needed.

## Install

Requires macOS, Git, and both desktop apps. Install the current `latest` release with one command:

```sh
curl -fsSL https://github.com/r13v/peer/releases/download/latest/install.sh | sh
```

The installer checks the archive's SHA-256 digest, puts `peer` in `~/.local/bin`, and installs the same discovery skill for Claude Code and Codex. Add `~/.local/bin` to `PATH` if needed, then restart the apps. To get the newest release later, run `peer update`; it updates the command and both skill copies. Every push to `main` replaces the single `latest` release.

To build from source instead, use Go 1.22 or newer: create `~/.local/bin`, run `go build -o "$HOME/.local/bin/peer" .`, and copy `skills/peer/SKILL.md` to `~/.claude/skills/peer/SKILL.md` and `~/.codex/skills/peer/SKILL.md`.

Open **local** chats in the same checkout, without separate worktrees. Ask one agent to write and the other to read and review. The writer runs `peer start --as claude` or `--as codex`; both agents load the corresponding instructions from the CLI.

## CLI

Run commands from anywhere inside the shared Git checkout:

```sh
peer skills writer
peer skills reader
peer update
peer status
peer start --as codex
peer send --as codex <<'MESSAGE'
Please challenge this approach before I edit.
MESSAGE
peer wait --as claude
peer log --follow
peer end --as codex
peer history
peer log --session SESSION_ID
peer log --session SESSION_ID > transcript.txt
```

`wait` returns one JSON message and marks it delivered, or `{"status":"timeout"}` after 90 seconds. Its timeout can be set up to 110 seconds, below Claude Code's default two-minute Bash limit. A completed chat cannot be sent to; end it before starting another task in the checkout.

The CLI stores private session files and an append-only `messages.jsonl` transcript under `~/Library/Application Support/peer/`. Set `PEER_HOME` to use another local directory. `log --follow` displays the dialogue live; `history` lists past session IDs. The transcript contains messages sent through this CLI, not the agents' private reasoning or tool output. You can give a saved session ID to an agent later and ask it to review the conversation and suggest specific changes to the workflow or skills.

This CLI does not inject prompts into idle desktop chats. Each participating agent must keep calling `wait` while a reply is needed. The reviewer role is an instruction and session record, not an operating-system restriction on file writes; use the app's read-only or Plan permission mode if that guarantee matters.
