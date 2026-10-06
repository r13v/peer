# peer for Claude Code

The `peer` plugin shows the rooms of the checkout in Claude Code: in the Code tab of the desktop app, in the terminal and in VS Code.

- **`/peer:peer TASK`** starts the work: the plugin's own skill runs `peer skills flow`, and as main, Claude reads the room through the plugin instead of `peer wait`. With the plugin, Claude Code does not need the standalone `peer` skill.

- **Pane.** It opens by itself when a room is active and there is room for it, and from the footer button.
  - One row of tabs: **Chat** with its message count, a tab for each member, ● while its agent runs and ○ once it stopped, and `⏹ end` at the right. A member's tab shows its model and state, a kick button and its log: its text, its commands and their results. Kick and end act on a second press: the first turns the button into `confirm` for three seconds.
  - **Chat** shows the newest message first. Each message is a card in its author's color, the time first, with Markdown, code blocks and links to the checkout's files, such as `main.go:42`. Notices from `peer` take one line, and a long message shows its first lines until you expand it. A timeline shows who wrote when (not in the terminal). The pane shows each new message unless you hold its keyboard.
- **Footer button.** At the right of the prompt footer, 👥 opens the pane, with the number of messages that came since the pane last showed; with several rooms, a numbered button for each, in the order of the pane's room list, opens the pane on that room.
- **Delivery to main.** When Claude Code is main of a room, the plugin reads the room for main and puts each batch of messages into the chat as a turn of its own: right away while Claude is idle, and right after the running turn while it works. These rows are hidden in the chat, since the toast, the pane and the footer's count show the messages; in the terminal and VS Code, ctrl+o shows them as the model reads them. Main does not run `peer wait`; the plugin answers a `wait` for the room itself, so the room has only one reader as main.
- **Toasts** when a member joins, exits or is kicked, and when a room ends.

The plugin runs `peer watch`, `peer memberlog` and `peer ack` from your `PATH`, so it needs a `peer` with these commands. After each delivery, `peer ack` moves main's cursor just past the last message in the chat, so a `peer wait` run after the plugin is turned off gets the rest.

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
