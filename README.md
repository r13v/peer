# peer

`peer` lets local coding agents work on one task in one Git checkout. The writer edits the code. The other agents are members with a role, such as `reader`, `test-expert` or `domain-expert`. They discuss the approach and review the diff. All messages stay on your machine. You do not need an MCP server or a model API key.

## Install

You need macOS or Linux (amd64 or arm64), Git, and at least two agents that can run shell commands, for example Claude Code and Codex. For a background member, install its CLI (`claude`, `codex` or [`pi`](https://github.com/earendil-works/pi)) and put it on your `PATH`.

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

Claude becomes the writer. It starts a room and runs Codex in the background as the reader. It also gives you a join prompt in the language of your chat, so you can add more agents later. See [How it works](#how-it-works) for the steps that follow.

To start from Codex, send `$peer <task>` in a local Codex chat. Codex becomes the writer and runs Claude Code as the reader. The agent that gets the task is always the writer.

To add experts, name them in the task, for example `/peer add CSV export; also invite a test expert and a docs expert`. The writer runs each one in the background with its own role. Roles are free-form names.

To add any agent yourself, for example Copilot, paste the writer's join prompt into that agent's chat in the same checkout. Change the role in it and add your own instructions after it. The agent can join at any time while the room is active, and it first reads the earlier messages.

To see a member's chat in its desktop app on macOS, ask the writer to add `--headed` to `peer invite`. The app must already be open. `peer` opens a new chat with the prompt filled in, but you must press Enter. If the app is not open, or the agent is `pi`, which has no desktop app, the member runs in the background.

To watch the conversation, run `peer` in a terminal.

## How it works

1. **Start.** The writer runs `peer start ROOM`. This creates a room for the task.
2. **Add members.** The writer runs `peer invite ROOM ROLE --as writer --agent codex` to start `codex`, `claude` or `pi` in the background in the same checkout. Any other agent runs `peer join ROOM ROLE`. The writer gets a notice from `peer` for each member that joins.
3. **Discuss.** The agents send messages with `peer send` and receive them with `peer wait`. A message goes to all participants, or to one with `--to ROLE`. `wait` returns one message, or a timeout after 90 seconds. The agent then calls `wait` again. With `--timeout 0`, `wait` waits until a message comes or the room ends. A writer whose agent can run a background command and wake when it exits, such as Claude Code, uses it between turns, so it reads what you send from `peer`. The agents write in the language that you use with the writer.
4. **Implement.** Only the writer edits files.
5. **Review.** The members inspect the diff and report findings. The writer fixes them and asks for another review.
6. **End.** The writer runs `peer end`. After this, nobody can send messages to the room. Start a new room for the next task.

A room ends in one of these ways:

- The writer runs `peer end`.
- In `peer`, you focus the room list, select the room, and press `x`.

A room does not end when a background member stops. The writer gets a notice from `peer` and can invite the same role again. Closing the writer's chat or quitting `peer` does not end a room.

`peer` does not type into idle chats. Each agent must call `wait` when it needs a reply. Several rooms can be active in one checkout at the same time.

### Member permissions

A background member gets an instruction not to edit files. It can run any shell command, such as tests or scripts. Codex and Claude Code run commands in their sandbox. By default, the sandbox lets commands write only the checkout, temp directories and the checkout's `peer` store. Commands can use the network, including `localhost`, so a member can send what it reads anywhere. The agent's user settings can widen these limits. Neither agent asks for approval.

- **Codex** runs in its `workspace-write` sandbox.
- **Claude Code** runs Bash in its sandbox and stops if the sandbox is not available. It gets all tools but Edit, Write and NotebookEdit, including WebFetch, WebSearch, subagents, tasks and LSP. It loads no MCP servers and ignores the checkout's `.claude` settings.
- **Pi** runs without a sandbox, because pi has none. It gets the `read`, `grep`, `find`, `ls` and `bash` tools, but not `edit` and `write`. It loads your global extensions and skills, uses your default model and ignores the checkout's `.pi` settings. Its shell commands and extensions keep the permissions of your account and can change any file that you can access.

Shell commands can still change files in the checkout, so the "do not edit" rule is only an instruction. If you need a guarantee, use the agent's read-only or Plan mode.

## Watch rooms

Run `peer` with no arguments. The left pane lists all rooms from all checkouts, active rooms first. The right pane shows the transcript of the selected room. The bottom pane shows the log of a background member, with the time of each message and command. The focused pane has a blue frame; click a pane to focus it. The right edge of each frame is its scrollbar. The footer lists the keys for the focused pane and the state of the room, and `?` opens all keys in a window over the panes. Shortcuts work with any keyboard layout in terminals that support the kitty keyboard protocol, and with the Russian layout in any terminal. Press `L` to switch to the next background member. `peer` works outside a checkout too. Press `i` to send a message as `user` to everyone in the room or to one participant. Each agent reads it on its next `peer wait`. The name `user` is reserved for these messages. Press `a` to ask the writer to add a member: pick `claude`, `codex` or `pi` and describe the role in a few words, such as `security reviewer`. The writer picks the role name, expands the description into a brief and runs `peer invite` on its next `peer wait`. On macOS, it sends a notification when the room that you watch ends.

| Key | Action |
| --- | --- |
| j/k, ↑/↓ | Select a room, or scroll the focused pane |
| Tab | Move focus to the next pane |
| Enter | Open the selected room's transcript |
| / then n/N | Search the focused pane |
| x x | End the selected active room (room list focused); the first x asks for confirmation in the footer |
| i | Write a message to the selected active room; Tab picks all or one participant, Enter sends, Esc cancels |
| a | Ask the writer of the selected active room to add a member; Tab switches the agent, Enter moves on to the role description and then sends, Esc cancels |
| ? | Show all keys |
| q | Quit |

## Commands

Run these commands inside the shared Git checkout.

| Command | Action |
| --- | --- |
| `peer start ROOM [--agent NAME]` | Start a room with you as the writer and print it as JSON |
| `peer invite ROOM ROLE --as writer --agent codex\|claude\|pi [--brief TEXT] [--headed]` | Add a member and start its agent |
| `peer join ROOM ROLE [--agent NAME]` | Join a room as a member |
| `peer send ROOM --as ROLE [--to ROLE]` | Send a message from stdin to all participants or to one |
| `peer wait ROOM --as ROLE [--timeout DURATION]` | Wait for one message, up to 90 seconds by default; `0` waits until one comes or the room ends |
| `peer end ROOM --as writer` | End the room |
| `peer status [ROOM]` | Show active rooms, or one room |
| `peer history` | List all rooms in this checkout |
| `peer log ROOM` | Print a transcript |
| `peer skills flow\|writer\|member` | Print the agent instructions |
| `peer update` | Update an installer copy of the CLI |
| `peer --version` | Print the version |

If the name given to `start` is already in use, `peer` adds `-2`, `-3`, and so on. Use the `id` from the JSON output in all later commands. Each role is used once in a room. For two members with the same focus, use `test-expert` and `test-expert-2`. The roles `writer`, `user` and `peer` are reserved.

## Storage and settings

`peer` keeps each room in `~/.peer/repos/CHECKOUT-HASH/sessions/ROOM/`. The transcript is `messages.jsonl`. It contains only the messages that the agents sent with `peer`, not their reasoning or tool output.

| Variable | Effect |
| --- | --- |
| `PEER_HOME` | Use a different store directory. Both agents must use the same value. |
| `PEER_EDITOR_URL` | Open file links from `peer log` in an editor, for example `vscode://file/{path}:{line}` |
| `NO_COLOR=1` | Turn off colors in `peer log` |
