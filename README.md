# peer

`peer` lets two local coding agents work on one task in one Git checkout. One agent writes the code. The other agent discusses the approach and reviews the diff. All messages stay on your machine. You do not need an MCP server or a model API key.

## Install

You need macOS or Linux (amd64 or arm64), Git, and two agents that can run shell commands, for example Claude Code and Codex. For a background reader, install its CLI (`claude` or `codex`) and put it on your `PATH`.

1. Install the CLI.

   On macOS, use Homebrew:

   ```sh
   brew install --cask r13v/apps/peer
   ```

   On Linux, use the installer. It verifies the release checksum and puts `peer` in `~/.local/bin`. Set `PEER_INSTALL_DIR` to use a different directory. Make sure that the directory is on your `PATH`.

   ```sh
   curl -fsSL https://github.com/r13v/peer/releases/latest/download/install.sh | sh
   ```

2. Install the skill for your agents. This step needs Node.js.

   ```sh
   npx skills add r13v/peer -g
   ```

3. Restart the agent apps.

To update, run the command that matches your install. These commands update only the CLI:

- Homebrew: `brew upgrade --cask peer`
- Installer: `peer update`

To update the skill, run `npx skills update peer -g`. Keep only one copy of `peer` on your `PATH`.

To build from source, use Go 1.27 or later: `go build -o "$HOME/.local/bin/peer" .`

## Quick start

Open a **local** Claude Code chat in your Git checkout. Do not use a separate worktree. Send your task:

```text
/peer add CSV export to the reports page
```

Claude becomes the writer. It starts a room and runs Codex in the background as the reader. See [How it works](#how-it-works) for the steps that follow.

To start from Codex, send `$peer <task>` in a local Codex chat. Codex becomes the writer and runs Claude Code as the reader. The agent that gets the task is always the writer.

To pair with a different agent, name it in the task, for example `/peer pair with Copilot: add CSV export`. The writer gives you a prompt. Send that prompt to the other agent in the same checkout.

To see the reader's chat in its desktop app on macOS, ask the writer to add `--headed` to `peer start`. The app must already be open. `peer` opens a new chat with the prompt filled in, but you must press Enter. If the app is not open, the reader runs in the background.

To watch the conversation, run `peer` in a terminal.

## How it works

1. **Start.** The writer runs `peer start ROOM --writer NAME --reader NAME`. This creates a room for the task. If the reader is `claude` or `codex`, `peer` starts it in the background in the same checkout.
2. **Discuss.** The agents send messages with `peer send` and receive them with `peer wait`. `wait` returns one message, or a timeout after 90 seconds. The agent then calls `wait` again.
3. **Implement.** Only the writer edits files.
4. **Review.** The reader inspects the diff and reports findings. The writer fixes them and asks for another review.
5. **End.** The writer runs `peer end`. After this, nobody can send messages to the room. Start a new room for the next task.

A room ends in one of these ways:

- The writer runs `peer end`.
- In `peer`, you focus the room list, select the room, and press `x`.
- The background reader stops for any reason. The writer's `wait` then returns instead of waiting for a reader that is gone.

Closing the writer's chat or quitting `peer` does not end a room.

`peer` does not type into idle chats. Each agent must call `wait` when it needs a reply. Several rooms can be active in one checkout at the same time.

### Reader permissions

A background reader gets an instruction not to edit files. It also gets these limits:

- **Codex** runs in its `workspace-write` sandbox with access to the `peer` store. It does not ask for approval.
- **Claude Code** can use only Read, Grep, Glob, Skill, and Bash for `peer` and read-only `git` commands. It denies all other requests.

Codex can still write files in the checkout. For Codex and for other readers, the "do not edit" rule is only an instruction. If you need a guarantee, use the agent's read-only or Plan mode.

## Watch rooms

Run `peer` with no arguments. The left pane lists all rooms from all checkouts, active rooms first. The right pane shows the transcript of the selected room. The bottom pane shows the background reader's log. `peer` works outside a checkout too. Press `i` to send a message as `user` to everyone in the room or to one participant. Each agent reads it on its next `peer wait`. The name `user` is reserved for these messages. On macOS, it sends a notification when the room that you watch ends.

| Key | Action |
| --- | --- |
| j/k, ↑/↓ | Select a room, or scroll the focused pane |
| Tab | Move focus to the next pane |
| Enter | Open the selected room's transcript |
| / then n/N | Search the focused pane |
| x | End the selected active room (room list focused) |
| i | Write a message to the selected active room; Tab picks all or one participant, Enter sends, Esc cancels |
| ? | Show all keys |
| q | Quit |

## Commands

Run these commands inside the shared Git checkout.

| Command | Action |
| --- | --- |
| `peer start ROOM --writer NAME --reader NAME [--headed]` | Start a room and print it as JSON |
| `peer send ROOM --as NAME` | Send a message from stdin |
| `peer wait ROOM --as NAME` | Wait up to 90 seconds for one message |
| `peer end ROOM --as NAME` | End the room |
| `peer status [ROOM]` | Show active rooms, or one room |
| `peer history` | List all rooms in this checkout |
| `peer log ROOM` | Print a transcript |
| `peer skills flow\|writer\|reader` | Print the agent instructions |
| `peer update` | Update an installer copy of the CLI |
| `peer --version` | Print the version |

If the name given to `start` is already in use, `peer` adds `-2`, `-3`, and so on. Use the `id` from the JSON output in all later commands. To pair two sessions of the same app, give them different names, for example `codex-main` and `codex-review`.

## Storage and settings

`peer` keeps each room in `~/.peer/repos/CHECKOUT-HASH/sessions/ROOM/`. The transcript is `messages.jsonl`. It contains only the messages that the agents sent with `peer`, not their reasoning or tool output.

| Variable | Effect |
| --- | --- |
| `PEER_HOME` | Use a different store directory. Both agents must use the same value. |
| `PEER_EDITOR_URL` | Open file links from `peer log` in an editor, for example `vscode://file/{path}:{line}` |
| `NO_COLOR=1` | Turn off colors in `peer log` |
