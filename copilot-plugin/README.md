# peer for Copilot CLI

The `peer` plugin for GitHub Copilot CLI delivers room messages to a Copilot main without `peer wait`, and shows a room on a canvas in the GitHub Copilot app. Without the plugin, Copilot works with the agent skill and `peer wait` as before.

- **The `peer` skill** starts the work: it runs `peer skills flow`. Copilot picks it when you ask agents to work together, or you start it with **`/peer TASK`**, a command of the plugin's extension. Copilot as main starts the room with `peer start`.
- **Delivery to main.** After a plain `peer start NAME` command, the plugin's extension starts `peer forward` for the room. It reads the room for main and puts each batch of messages into the Copilot session as a queued turn: right away while Copilot is idle, and after the running turn while it works. A note after `peer start` tells the model that the plugin delivers the room; `peer wait` for the room is refused while it does. A `peer start` inside a longer shell command, such as `peer start NAME && peer invite …`, is not recognized: main then reads the room with `peer wait`.
- **Canvas.** In the GitHub Copilot app, the `peer room` canvas shows the chat of a room that the session leads, newest first. It is read-only.

The extension API is experimental: Copilot loads the extension only with `--experimental` or after `/experimental on`. Without it, only the skill works, without `/peer`, and main runs `peer wait`.

## How delivery works

- `peer forward ID --as main --copilot-session UUID` holds main's delivery for the room, as for Codex, and hands each batch to the extension over stdio: a line `{"status":"batch","batch":ID,"text":TEXT}`. The extension queues the text in the session and answers `{"ack":ID}`, or `{"nack":ID,"error":TEXT}` when the queue fails. Only an ack marks the batch read; a nack brings it again after a pause.
- No answer within 15 seconds, an answer for another batch or a line that is not one stops forward with an error, and a closed stdin stops it quietly. The batch stays unread in each case, and main's delivery is free again: the extension tells the model to read the room with `peer wait`.
- Delivery is at least once: a crash between a queued batch and its ack brings the batch again. Each turn names its batch, so a repeat shows.
- When the room ends, forward hands over its last messages with a note that the room has ended, marks this with `forward-main.done` in the room's directory, and exits.
- The extension keeps, per Copilot session, the rooms it leads in the plugin's data directory. When a session is resumed, it starts forward again for a room that is still active, or that has ended before its end was delivered. A new session after `/clear` gets no rooms of the old one.

## Requirements

- Copilot CLI 1.0.97, the version the plugin was checked with, or a later one with the same extension API
- `peer` with `forward --copilot-session` and `copilot-start` on your `PATH`
- For the canvas, the GitHub Copilot app

## Install

```bash
copilot plugin install r13v/peer:copilot-plugin
```

Then turn on experimental features with `/experimental on`, or start Copilot with `--experimental`.

If you installed the agent skill for Copilot before, remove it, so that only the plugin's `peer` skill is left:

```bash
npx skills remove peer -g -a github-copilot
```

The plugin's version is the CLI's: each release of `peer` sets it, so `copilot plugin update peer` brings the plugin that goes with the new CLI.

To install `peer` itself, see the [main README](../README.md).

## Develop

```bash
copilot --experimental --plugin-dir copilot-plugin
```

The extension writes its diagnostics to stderr, which goes to Copilot's log.
