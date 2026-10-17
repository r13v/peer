# peer for Claude Code

The `peer` plugin shows a session's peer rooms in Claude Code: in the Code tab of the desktop app, in the terminal and in VS Code. A session's rooms are the ones it started with `peer start`, leads with `peer wait --as main` or joined with a `peer join` command of its own; after a rewind or a resume the plugin finds them again in the conversation; another session's rooms in the same checkout stay out of it, and the `peer` TUI shows them all. Outside a Git checkout, the plugin shows nothing.

- **`/peer:peer TASK`** starts the work: the plugin's own skill runs `peer skills flow`, and as main, Claude reads the room through the plugin instead of `peer wait`. With the plugin, Claude Code does not need the standalone `peer` skill.

- **Pane.** It opens by itself when a room is active and there is room for it, and from the footer button.
  - One row of tabs: **Chat**, a tab for each member, ● while its agent runs and ○ once it stopped, and `⏹ end` at the right. A member's tab shows its model and state, a kick button and its log: its text, its commands and their results. Kick and end act on a second press: the first turns the button into `confirm` for three seconds.
  - **New room.** A pane without a room shows a task field; with a room, `+ new room` in the row of tabs shows it. A task sent with Enter runs `/peer:peer TASK`, and the pane shows the task in place of the field until the room starts. Where the surface has no fields, `+ new room` puts `/peer:peer ` in the prompt box instead.
  - **Keys.** While the pane holds the keyboard (ctrl+x tab or a click): `1` chat, `2` boards, `3`–`9` the members' tabs, `n` new room, `e` end, `k` kick on a member's tab; end and kick still take a second press.
  - **Replay.** A member's tab with file edits in its log has `replay N edits` (`r`): one step per edit, with `h` and `l` to go back and on. A step shows the file and the file's uncommitted diff in the member's checkout (its worktree, if it has one) now, not the step's own diff: the log keeps an edit's path, and cuts its input at 200 characters, so an edit whose input was cut is left out.
  - **Chat** shows the newest message first. Each message is a card in its author's color, the time first, with Markdown, code blocks and links to the checkout's files, such as `main.go:42`. Notices from `peer` take one line, and a long message shows its first lines until you expand it. A timeline shows who wrote when (not in the terminal). The pane shows each new message unless you hold its keyboard.
- **Boards.** The pane's **boards** tab lists the boards of the room it shows, from `peer board list`: each with its title, a link to open it in the browser (start `peer` if the link does not open) and a preview. The preview is a schematic of the frames, nodes and edges that the plugin lays out itself, so a board nobody opened in the browser has one too; in the terminal, the same schematic drawn in cells (frames as boxes, cards as blocks of their color, without edges) and a list of frames with their node and open-thread counts. See [docs/BOARD.md](../docs/BOARD.md).
- **Footer button.** At the right of the prompt footer, `👀 Peer` is always there, with or without a room. It shows the number of messages in the session's rooms that came since the pane last showed them, opens the pane, and closes it when the pane already shows.
- **Delivery to main.** When Claude Code is main of a room, the plugin reads the room for main and puts each batch of messages into the chat as a turn of its own: right away while Claude is idle, and right after the running turn while it works. These rows are hidden in the chat, since the toast, the pane and the footer's count show the messages; in the terminal and VS Code, ctrl+o shows them as the model reads them. Main does not run `peer wait`; the plugin answers a `wait` for the room itself, so the room has only one reader as main.
- **Messages in the transcript.** A Bash row that only runs `peer send ID --as ROLE [--to ROLE] --text TEXT` is drawn as the message card, in its author's color. A line the shell changes first (`$`, `` ` ``, `\`, `<`, `>`, more commands) keeps its row, so a card shows only what was sent.
- **`/room`** opens or closes the pane. In the prompt box, `@` offers the members of the room the pane shows.
- **Delivery turns.** While Claude reads a delivery, the spinner says how many messages and from whom; after it, the empty prompt box suggests `Tell ROLE: ` for the last author (Tab takes it).
- **Permission dialog.** For `peer end` and `peer kick`, a line under the dialog says which members' agents still run or which agent the kick stops.
- **Toasts** when a member joins, exits or is kicked, and when a room ends. The plugin raises no system notifications.

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

### See a change in a running session

The installed plugin runs the copy that `claude plugin install` or `claude plugin update` made, so an edit in the checkout reaches it only with a release. To see an edit at once, load `plugin` from the checkout into the session.

1. Disable the installed plugin: `claude plugin disable peer@r13v`. Two copies would both read a room for main, which breaks one reader per role, and both would draw a footer button.
2. Load the checkout's `plugin`:
   - **Terminal:** `claude --plugin-dir plugin` from the checkout. The session watches the folder: a saved file reloads the hooks module.
   - **Desktop app (Code tab), new session:** name the absolute path of `plugin` in `CLAUDE_CODE_PLUGIN_DIRS` in the `env` block of `~/.claude/settings.json`. Each new session loads it when it starts.
   - **Desktop app, running session:** ask Claude to live-reload the plugin from this checkout. Claude loads its `plugin-authoring` skill and links `plugin` into the session's mods folder, `~/.claude/dev-mods/SESSION_ID/`; Claude Code then asks once "Enable hot reloading for this session?". Choose `Enable for this session`: the plugin loads when the turn ends, and each later edit reloads it when the turn that made the edit ends.
3. When you are done, run `claude plugin enable peer@r13v` and remove the link or the `CLAUDE_CODE_PLUGIN_DIRS` entry.

A reload starts the module again: its own variables, such as queued deliveries, start empty. Rooms lose no messages, because `peer ack` keeps main's cursor in the store.

### Check against a new Claude Code

Claude Code updates can break the plugin. Two checks catch it:

- **Typecheck** (`npm run typecheck:plugin`) checks the hooks module and the tests against the types that Claude Code writes into `plugin/.claude-plugin/types` (`claude-code` and `claude-code-tools`, not `claude-code-mcp`, which depends on the machine's MCP servers). Only an interactive load of the plugin writes them, not `claude -p`, and the first line of `claude-code/index.d.ts` names the Claude Code that wrote them. When that is not `claude --version`, [`scripts/typecheck-plugin.ts`](../scripts/typecheck-plugin.ts) loads the plugin once in a pseudo-terminal, as a session that is not logged in, with a temporary `HOME`, waits for the new types (a few seconds) and stops that session. So the typecheck always uses the types of the installed CLI, and nothing is copied or committed. Without `claude` on `PATH` it checks the types there are, or prints SKIPPED.
- **Tests** (`npm run test:plugin`) run `plugin/tests/*.test.ts` in the kit inside the `claude` binary, without a login. They catch changes of the hook API and of the plugin's logic. They do not catch engine semantics: everything below the plugin is a stub of the test. `engine.test.ts` pins the hooks that keep one reader as main: the plugin answers main's `peer wait` and denies a line that mixes it with other commands.

`npm run engine-check` runs both once per `claude --version` and committed plugin tree (`git rev-parse HEAD:plugin`). It keeps the plugin trees that passed under this CLI in `~/.cache/peer/engine-checked`, one line each, and drops those of an older CLI: a new commit of the plugin or a pull is checked again under the same CLI, while worktrees with different plugins do not check again in turn; `--force` checks again. Uncommitted edits are for the pre-commit typecheck. A checkout without `node_modules` prints one line and skips the check, since `tsc` comes from there. On a failure it prints the step and the last 30 lines of its output and exits 1. `PEER_ENGINE_CHECK` set in the environment skips the check, so a Claude Code session that a step starts does not run it again.

The [Claude Code workflow](../.github/workflows/engine.yml) runs `claude plugin validate`, the typecheck and the tests against the latest Claude Code every day and on demand, so a release that breaks the plugin fails there before you update; GitHub mails the failure.

To run `engine-check` when a session starts, add a hook to your own `~/.claude/settings.json`. It runs in the session's own checkout, `$CLAUDE_PROJECT_DIR`, so a worktree checks its own plugin against the types it has the CLI write; a hard-coded path would check another checkout's code. A project's `.claude/settings.local.json` is not committed, so new worktrees do not have it; and the file test skips other repositories. Claude Code puts the hook's output into the session only on exit 0, hence `|| true`:

```json
"hooks": { "SessionStart": [{ "hooks": [{ "type": "command", "command": "cd \"$CLAUDE_PROJECT_DIR\" && [ -f scripts/engine-check.ts ] && node scripts/engine-check.ts || true", "timeout": 180 }] }] }
```
