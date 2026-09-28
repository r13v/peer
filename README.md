# peer

Local CLI for a Claude Code Desktop and Codex Desktop pair working in one Git checkout. One agent writes; the other discusses and reviews. Messages and the transcript stay on this Mac. No MCP server or model API is needed.

## Install

Requires macOS, Git, and both desktop apps. Install the current `latest` release with one command:

```sh
curl -fsSL https://github.com/r13v/peer/releases/download/latest/install.sh | sh
```

The installer checks the archive's SHA-256 digest and puts `peer` in `~/.local/bin`. Add `~/.local/bin` to `PATH` if needed. To get the newest CLI later, run `peer update`. Every push to `main` replaces the single `latest` release.

Install the skill separately for Claude Code and Codex with Node.js and `npx skills`:

```sh
npx skills add r13v/peer --skill peer -g -a claude-code -a codex -y
```

This command installs only the skill; `peer` must also be on `PATH`. Restart the apps after installation. `peer update` updates only the CLI; update the skill with `npx skills update peer -g`.

To build the CLI from source instead, use Go 1.22 or newer: create `~/.local/bin` and run `go build -o "$HOME/.local/bin/peer" .`.

## Pair on a task

Open one **local** chat in Claude Code Desktop's Code tab and one in Codex Desktop, both in the same Git checkout without separate worktrees. Keep both chats open. Send this to Claude first, replacing the example task with yours:

```text
/peer
You are the writer (Claude); Codex is the reader. Pair with Codex on this task: add CSV export to the reports page. Discuss the approach through peer before editing. Then implement it, ask Codex to review the diff, address its findings, and end the peer session.
```

Then send this to Codex:

```text
$peer
You are the reader for Claude's peer session in this checkout. Discuss the approach through peer, then review Claude's diff and send concrete findings through peer. Do not edit files. Keep waiting for replies until the review is closed.
```

Claude starts the session with `peer start --as claude`; the agents use `peer send` and `peer wait` to talk. To watch their conversation, run `peer log --follow` in a terminal in the checkout.

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
