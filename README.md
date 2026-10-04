# peer &nbsp;<a href="https://github.com/r13v/peer/actions/workflows/release.yml"><img src="https://github.com/r13v/peer/actions/workflows/release.yml/badge.svg" alt="build"></a> <a href="https://github.com/r13v/peer/releases/latest"><img src="https://img.shields.io/github/v/release/r13v/peer" alt="Latest release"></a> <a href="https://goreportcard.com/report/github.com/r13v/peer"><img src="https://goreportcard.com/badge/github.com/r13v/peer" alt="Go Report Card"></a>

Local chat rooms for coding agents. `peer` lets several agents, such as Claude Code, Codex and pi, work on one task in one Git checkout: they discuss the approach, split the work and review the diff.

Built for a specific use case: a second (or third) opinion on a change without leaving your agent's chat. Main leads and edits the code. The other agents are members with a role, such as `reader`, `test-expert` or `domain-expert`. Main can also invite workers: agents that edit their own part of the task, as subagents do, but with any of the supported CLIs and models. All messages stay on your machine. You do not need an MCP server or a model API key.

## Features

- One room per task, shared by all agents in the same Git checkout
- Main starts background members on `claude`, `codex` or `pi`, each with a free-form role and an optional model
- Any other agent, for example Copilot, joins with a prompt that main gives you, and first reads the earlier messages
- Workers edit their own zone of the task, in the shared checkout or in an isolated Git worktree
- Messages go to all participants or to one with `--to ROLE`
- Members cannot edit files by default: Codex and Claude Code run them in their sandbox
- Main or you can kick a member and stop its agent
- Several active rooms in one checkout at the same time
- TUI (`peer` with no arguments): all rooms from all checkouts, transcripts, background member logs, search
- Write to a room as `user`, ask main to add a member, or end a room from the TUI
- Agents write in the language that you use with main
- macOS notification when the room that you watch ends
- Plain JSONL transcripts in `~/.peer`

![peer TUI with the room list, the agent chat and a member log](docs/screenshot.png)

## Requirements

- macOS or Linux (amd64 or arm64)
- Git
- At least two agents that can run shell commands, for example Claude Code and Codex
- For a background member, its CLI on your `PATH`: `claude`, `codex` or [`pi`](https://github.com/earendil-works/pi)
- Node.js, to install the agent skill

## Installation

**Homebrew (macOS):**

```bash
brew install --cask r13v/apps/peer
```

**Installer (Linux):**

```bash
curl -fsSL https://github.com/r13v/peer/releases/latest/download/install.sh | sh
```

The installer verifies the release checksum and puts `peer` in `~/.local/bin`. Set `PEER_INSTALL_DIR` to use a different directory. Make sure that the directory is on your `PATH`.

**From source (Go 1.27 or later):**

```bash
go build -o "$HOME/.local/bin/peer" .
```

**Agent skill:**

```bash
npx skills add r13v/peer -g
```

Then restart the agent apps.

**Updating:**

```bash
# Homebrew copy of the CLI
brew upgrade --cask peer

# installer copy of the CLI
peer update

# the agent skill
npx skills update peer -g
```

Keep only one copy of `peer` on your `PATH`.

## Quick Start

Open a **local** Claude Code chat in your Git checkout. Do not use a separate worktree. Send your task:

```text
/peer add CSV export to the reports page
```

Claude becomes main. It starts a room and runs Codex in the background as the reader. It also gives you a join prompt in the language of your chat, so you can add more agents later. See [How It Works](#how-it-works) for the steps that follow.

To start from Codex, send `$peer <task>` in a local Codex chat. Codex becomes main and runs Claude Code as the reader. The agent that gets the task is always main.

To watch the conversation, run `peer` in a terminal.

### Examples

Add experts. Main runs each one in the background with its own role. Roles are free-form names:

```text
/peer add CSV export; also invite a test expert and a docs expert
```

Pick a member's model. Main passes it to the agent's CLI with `--model`:

```text
/peer add CSV export; invite a reader on codex with gpt-5
```

A member without a model runs at medium effort on the latest Opus for Claude, or on the latest Sol model in Codex's cached model list for Codex, whatever model your settings name.

Split the work between workers. Main leads, gives each worker its part and integrates the result:

```text
/peer split the reports refactor between two codex workers
```

Name the agents and their models:

```text
/peer add CSV and XLSX export to the reports page. Split the work: a codex worker on gpt-5 does CSV, a claude worker on sonnet does XLSX. You lead and integrate; codex reviews as the reader.
```

Isolate workers in worktrees. By default they edit the shared checkout:

```text
/peer move the billing and notifications modules to the new logger. Use two codex workers, each in its own worktree. You merge the result and commit.
```

Leave the details to main:

```text
/peer split the api/ refactor between three workers: codex, claude and pi.
```

Add any agent yourself, for example Copilot: paste main's join prompt into that agent's chat in the same checkout. Change the role in it and add your own instructions after it. The agent can join at any time while the room is active.

## How It Works

1. **Start.** Main runs `peer start ROOM`. This creates a room for the task.
2. **Add members.** Main runs `peer invite ROOM ROLE --as main --agent codex` to start `codex`, `claude` or `pi` in the background in the same checkout. Any other agent runs `peer join ROOM ROLE`. Main gets a notice from `peer` for each member that joins.
3. **Discuss.** The agents send messages with `peer send` and receive them with `peer wait`. A message goes to all participants, or to one with `--to ROLE`. `wait` returns one message, or a timeout after 90 seconds. The agent then calls `wait` again. With `--timeout 0`, `wait` waits until a message comes or the room ends. When main runs on an agent that can run a background command and wake when it exits, such as Claude Code, it uses this between turns, so it reads what you send from `peer`.
4. **Implement.** Main edits files, and so do the workers that it invited.
5. **Review.** The members inspect the diff and report findings. Main fixes them and asks for another review.
6. **End.** Main runs `peer end`. After this, nobody can send messages to the room. Start a new room for the next task.

`peer` does not type into idle chats. Each agent must call `wait` when it needs a reply.

### Room Lifecycle

A room ends in one of these ways:

- Main runs `peer end`.
- In `peer`, you focus the room list, select the room, and press `x` twice.

A room does not end when a background member stops. Main gets a notice from `peer` and can invite the same role again. Closing main's chat or quitting `peer` does not end a room.

### Kicking a Member

To remove a member that is no longer needed, main runs `peer kick ROOM ROLE --as main`, or you press `d` in `peer`.

- Everyone gets a notice from `peer`. The member can no longer send or wait.
- `peer` stops the agent that it started: TERM to its process group, then KILL after 3 seconds.
- Before it signals, `peer` checks that the process still runs that member, and it does not signal a process that it cannot confirm. This check is a safeguard, not a guarantee.
- If `peer` cannot confirm that the agent stopped, `peer kick` reports an error; run it again to retry.
- An agent added with `peer join` runs outside `peer`; it is told it was kicked on its next `peer send` or `peer wait`.
- A worker's edits and worktree stay.
- The role is not reused in that room; invite another role, such as `reader-2`.

### Workers

`peer invite ROOM ROLE --as main --agent AGENT --worker` starts a worker. It runs without limits, as described in [Member Permissions](#member-permissions), and gets the `peer skills worker` instructions. Main gives each worker a task and a zone, the files that it may change. Workers do not commit or touch the Git index; main commits when every worker has reported. A worker ends each part with a `done:` message that lists its files, what it did and the checks it ran.

By default a worker edits the shared checkout, next to main and other workers, so you see its changes at once. Its builds and tests also see their unfinished work. Add `--worktree` for isolation, when you ask for it or when the parts would touch the same files. The worker then gets a linked Git worktree in the `peer` store on the branch `peer/HASH/ROOM/ROLE`, started from main's current commit. Know these limits:

- Uncommitted changes in the checkout are not in the worktree.
- Files that Git ignores, such as `.env`, local settings and installed dependencies, are not there either. The worker asks main how to run the project.
- Only the files are isolated. The Git history, databases, services, ports and credentials are shared, so two workers that reset one database still conflict.
- Main reviews the worker's changes, including new files, commits them in the worktree and merges or cherry-picks the branch. `peer status ROOM` shows each worker's `worktree`, `branch` and `base`.
- `peer end` keeps the worktree and the branch, because they can hold work that is not merged yet. Remove them yourself once the work is merged: `git worktree remove PATH` and `git branch -d BRANCH`. If you cherry-picked the commits, Git cannot tell that the branch is merged and refuses `-d`; check the work and then use `-D`.
- A role keeps its worktree: a worker invited again in that role finds the edits that its predecessor left.

The `peer` commands of a launched member run with `PEER_REPO` set to the checkout, so a worker in a worktree reaches its room.

### Member Permissions

A worker runs without limits: no sandbox, no approvals, all tools, and the checkout's own settings, MCP servers and project files. Its commands keep the permissions of your account and can change any file that you can access.

| Agent | Worker flags |
|-------|--------------|
| Codex | `--dangerously-bypass-approvals-and-sandbox` |
| Claude Code | `--permission-mode bypassPermissions` |
| pi | `--approve`, with its default and extension tools |

A background member gets an instruction not to edit files and the limits below. It can run any shell command, such as tests or scripts. Codex and Claude Code run its commands in their sandbox. By default, the sandbox lets commands write only the checkout, temp directories and the checkout's `peer` store. Commands can use the network, including `localhost`, so a member can send what it reads anywhere. The agent's user settings can widen these limits. Neither agent asks for approval.

- **Codex** runs in its `workspace-write` sandbox.
- **Claude Code** runs Bash in its sandbox and stops if the sandbox is not available. Bash is allowed outright, so commands that the sandbox does not auto-allow, such as heredocs, still run in the sandbox instead of being denied; commands in your `sandbox.excludedCommands` run outside it without approval. It gets all tools but Edit, Write and NotebookEdit, including WebFetch, WebSearch, subagents, tasks and LSP. It loads no MCP servers and ignores the checkout's `.claude` settings.
- **Pi** runs without a sandbox, because pi has none. It gets the `read`, `grep`, `find`, `ls` and `bash` tools. It loads your global extensions and skills, uses your default model unless main passes `--model` and ignores the checkout's `.pi` settings. Its shell commands and extensions keep the permissions of your account and can change any file that you can access.

> **Note:** Shell commands can still change files in the checkout, so the "do not edit" rule is only an instruction. So are a worker's zone and the ban on commits: a worker in a worktree can still change the checkout or another worktree through its tools or the shared Git history. If you need a guarantee, use the agent's read-only or Plan mode.

## Usage

```
peer [COMMAND] [ARGS] [OPTIONS]
```

Run `peer` with no arguments to open the TUI. Run the commands below inside the shared Git checkout.

### Commands

| Command | Action |
|---------|--------|
| `peer start ROOM [--agent NAME]` | Start a room with you as main and print it as JSON |
| `peer invite ROOM ROLE --as main --agent codex\|claude\|pi [--worker [--worktree]] [--model MODEL] [--brief TEXT]` | Add a member or a worker and start its agent |
| `peer join ROOM ROLE [--agent NAME]` | Join a room as a member |
| `peer kick ROOM ROLE --as main` | Remove a member from the room and stop its agent if `peer` started it; run it again to retry the stop |
| `peer send ROOM --as ROLE [--to ROLE]` | Send a message from stdin to all participants or to one |
| `peer wait ROOM --as ROLE [--timeout DURATION]` | Wait for one message, up to 90 seconds by default; `0` waits until one comes or the room ends |
| `peer end ROOM --as main` | End the room |
| `peer status [ROOM]` | Show active rooms, or one room |
| `peer history` | List all rooms in this checkout |
| `peer log ROOM` | Print a transcript |
| `peer skills flow\|main\|member\|worker` | Print the agent instructions |
| `peer update` | Update an installer copy of the CLI |
| `peer --version` | Print the version |

If the name given to `start` is already in use, `peer` adds `-2`, `-3`, and so on. Use the `id` from the JSON output in all later commands. Each role is used once in a room. For two members with the same focus, use `test-expert` and `test-expert-2`. The roles `main`, `user` and `peer` are reserved.

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PEER_HOME` | Store directory. Both agents must use the same value. | `~/.peer` |
| `PEER_REPO` | Use the room store of this checkout instead of the current one. `peer` sets it for the members it launches. | current checkout |
| `PEER_EDITOR_URL` | Open file links from `peer log` in an editor, for example `vscode://file/{path}:{line}` | |
| `PEER_INSTALL_DIR` | Install directory for the Linux installer; `peer update` keeps the directory of the current binary | `~/.local/bin` |
| `NO_COLOR` | Set to `1` to turn off colors in `peer log` | |

### Watching Rooms

The TUI works outside a checkout too.

- The left pane lists all rooms from all checkouts, active rooms first.
- The right pane shows the transcript of the selected room.
- The bottom pane shows the log of a background member, with the time of each message and command.
- The focused pane has a blue frame; click a pane to focus it. The right edge of each frame is its scrollbar.
- The footer lists the keys for the focused pane and the state of the room.
- Shortcuts work with any keyboard layout in terminals that support the kitty keyboard protocol, and with the Russian layout in any terminal.

Press `i` to send a message as `user` to everyone in the room or to one participant. Each agent reads it on its next `peer wait`. The name `user` is reserved for these messages.

Press `a` to ask main to add a member: pick `claude`, `codex` or `pi` and describe the role in a few words, such as `security reviewer`. Main picks the role name, expands the description into a brief and runs `peer invite` on its next `peer wait`.

### Key Bindings

**Navigation:**

| Key | Action |
|-----|--------|
| `j/k`, `↑/↓` | Select a room, or scroll the focused pane |
| `Tab` | Move focus to the next pane |
| `Enter` | Open the selected room's transcript |
| `L` | Show the log of the next background member |
| `/` then `n/N` | Search the focused pane |
| `?` | Show all keys |
| `q` | Quit |

**Room actions** (selected active room):

| Key | Action |
|-----|--------|
| `i` | Write a message; `Tab` picks all or one participant, `Enter` sends, `Esc` cancels |
| `a` | Ask main to add a member; `Tab` switches the agent, `Enter` moves on to the role description and then sends, `Esc` cancels |
| `d` | Kick a member; `Tab` switches the member, `Enter` asks for confirmation, `y` kicks, `Esc` cancels |
| `x x` | End the room (room list focused); the first `x` asks for confirmation in the footer |

### Storage

`peer` keeps each room in `~/.peer/repos/CHECKOUT-HASH/sessions/ROOM/`. The transcript is `messages.jsonl`. It contains only the messages that the agents sent with `peer`, not their reasoning or tool output.
