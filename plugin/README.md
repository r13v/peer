# peer for Claude Code

The `peer` plugin shows a session's peer rooms in Claude Code: in the Code tab of the desktop app, in the terminal and in VS Code. A session's rooms are the ones it started with `peer start`, leads with `peer wait --as main` or joined with a `peer join` command of its own; after a rewind or a resume the plugin finds them again in the conversation; another session's rooms in the same checkout stay out of it, and the `peer` TUI shows them all. Outside a Git checkout, the plugin shows nothing.

- **`/peer:peer TASK`** starts the work: the plugin's own skill runs `peer skills flow`, and as main, Claude reads the room through the plugin instead of `peer wait`. With the plugin, Claude Code does not need the standalone `peer` skill.

- **Pane.** It opens by itself when a room is active and there is room for it, and from the footer button.
  - One row of tabs: **Chat** with its message count, a tab for each member, ● while its agent runs and ○ once it stopped, and `⏹ end` at the right. A member's tab shows its model and state, a kick button and its log: its text, its commands and their results. Kick and end act on a second press: the first turns the button into `confirm` for three seconds.
  - **New room.** A pane without a room shows a task field; with a room, `+ new room` in the row of tabs shows it. A task sent with Enter runs `/peer:peer TASK`, and the pane shows the task in place of the field until the room starts. Where the surface has no fields, `+ new room` puts `/peer:peer ` in the prompt box instead.
  - **Chat** shows the newest message first. Each message is a card in its author's color, the time first, with Markdown, code blocks and links to the checkout's files, such as `main.go:42`. Notices from `peer` take one line, and a long message shows its first lines until you expand it. A timeline shows who wrote when (not in the terminal). The pane shows each new message unless you hold its keyboard.
- **Boards.** The pane's **boards** tab lists the boards of the room it shows, from `peer board list`: each with its title, a link to open it in the browser (start `peer` if the link does not open) and a preview. The preview is a schematic of the frames, nodes and edges that the plugin lays out itself, so a board nobody opened in the browser has one too; in the terminal, a list of frames with their node and open-thread counts. See [docs/BOARD.md](../docs/BOARD.md).
- **Footer button.** At the right of the prompt footer, `👀 peer` is always there, with or without a room. It shows the number of messages in the session's rooms that came since the pane last showed them, opens the pane, and closes it when the pane already shows.
- **Delivery to main.** When Claude Code is main of a room, the plugin reads the room for main and puts each batch of messages into the chat as a turn of its own: right away while Claude is idle, and right after the running turn while it works. These rows are hidden in the chat, since the toast, the pane and the footer's count show the messages; in the terminal and VS Code, ctrl+o shows them as the model reads them. Main does not run `peer wait`; the plugin answers a `wait` for the room itself, so the room has only one reader as main.
- **Toasts** when a member joins, exits or is kicked, and when a room ends.

The plugin's version is the CLI's: each release of `peer` sets it, so `claude plugin update` brings the plugin that goes with the new CLI. The plugin runs `peer watch`, `peer memberlog` and `peer ack` from your `PATH`, so it needs a `peer` with these commands. After each delivery, `peer ack` moves main's cursor just past the last message in the chat, so a `peer wait` run after the plugin is turned off gets the rest. When `peer ack` fails, for example with an older `peer` earlier on `PATH`, the pane shows why in red; the plugin tries that room again and goes on delivering the other rooms.

## Install

```bash
claude plugin marketplace add r13v/peer
claude plugin install peer@r13v
```

If you installed the standalone skill for Claude Code before, remove it there and keep it for the other agents:

```bash
npx skills remove peer -g -a claude-code
```

To install `peer` itself, see the [main README](../README.md).

## Develop

```bash
claude plugin validate plugin
claude plugin test plugin
claude --plugin-dir plugin
```

### Check against a new Claude Code

Claude Code updates can break the plugin. Two checks catch it:

- **Typecheck** (`npm run typecheck:plugin`) checks the hooks module and the tests against the types that Claude Code writes into `plugin/.claude-plugin/types` (`claude-code` and `claude-code-tools`, not `claude-code-mcp`, which depends on the machine's MCP servers). Only an interactive load of the plugin writes them: `claude --plugin-dir plugin` in a terminal, or the desktop app with the plugin. `claude -p` does not. The first line of `claude-code/index.d.ts` names the Claude Code that wrote them, which can differ from `claude --version` when the desktop app and the CLI differ.
- **Tests** (`npm run test:plugin`) run `plugin/tests/*.test.ts` in the kit inside the `claude` binary, without a login. They catch changes of the hook API and of the plugin's logic. They do not catch engine semantics: everything below the plugin is a stub of the test. `engine.test.ts` pins the hooks that keep one reader as main: the plugin answers main's `peer wait` and denies a line that mixes it with other commands.

`npm run engine-check` runs both once per pair of versions: the Claude Code that wrote the types and `claude --version`. It keeps the last pair that passed in `~/.cache/peer/engine-checked`, so the stamp belongs to the machine, not to the checkout; `--force` checks again. On a failure it prints the step and the last 30 lines of its output and exits 1. When the two versions differ, it says that the types of the CLI were not checked. `PEER_ENGINE_CHECK` set in the environment skips the check, so a Claude Code session that a step starts does not run it again.

The [Claude Code workflow](../.github/workflows/engine.yml) runs `claude plugin validate` and the tests against the latest Claude Code every day and on demand, so a release that breaks the plugin fails there before you update; GitHub mails the failure. It cannot run the typecheck, since CI has no interactive load to write the types.

To run `engine-check` when a session starts, add a hook to your own `.claude/settings.local.json`. Claude Code puts the hook's output into the session only on exit 0, hence `|| true`:

```json
"hooks": { "SessionStart": [{ "hooks": [{ "type": "command", "command": "cd \"$CLAUDE_PROJECT_DIR\" && node scripts/engine-check.ts || true", "timeout": 180 }] }] }
```
