# peer for Codex

The `peer` plugin for Codex delivers room messages to a Codex main without `peer wait`. Without the plugin, Codex works with the agent skill and `peer wait` as before.

- **The `peer` skill** starts the work: it runs `peer skills flow`. Codex lists it as `peer (peer)`. Codex as main starts the room with `peer start`.
- **Delivery to main.** After a plain `peer start NAME` command, a hook starts `peer forward` for the room in the background. It reads the room for main and puts each batch of messages into the Codex session with `codex queue`: right away while Codex is idle, and after the running turn while it works. A hook note tells the model that forward delivers the room; `peer wait` for the room is refused while forward runs. A `peer start` inside a longer shell command, such as `peer start NAME && peer invite …`, is not recognized: main then reads the room with `peer wait`.

There is no pane, footer button or toast: Codex has no plugin API for them. Watch the room in the `peer` TUI.

## How delivery works

- `peer forward ID --as main --codex-thread UUID` reads a batch under the store's lock, runs `codex queue` without it, and moves main's cursor only if no one moved it meanwhile. A `codex queue` that fails or runs past 15 seconds is killed and tried again later; it never holds up other rooms.
- Delivery is at least once: a crash between a queued batch and its mark brings the batch again. Each turn names its batch, so a repeat shows.
- When the room ends, forward delivers its last messages with a note that the room has ended, marks this with `forward-main.done` in the room's directory, and exits.
- The hooks keep, per Codex session, the rooms it leads in the plugin's data directory. When a session is resumed, `SessionStart` starts forward again for a room that is still active, or that has ended before its end was delivered, unless forward still runs.
- Nothing but the room's end stops forward. In Codex, `/exit` only disconnects from a session, which goes on running queued turns, and Codex fires `SessionEnd` when a session is resumed; so the plugin has no `SessionEnd` hook.
- forward's output is in `forward-main.log` in the room's directory under `~/.peer`.

To turn the plugin off, remove it with `codex plugin remove peer`, and stop a running forward with `kill` or by ending the room. Batches already queued in Codex can still run as turns after that, and are marked read.

## Requirements

- Codex CLI 0.160 or later, with `codex queue`
- `peer` with `forward` and `codex-hook` on your `PATH`
- A Codex session ID that is a UUID; for anything else the hooks do nothing, and main runs `peer wait`

## Install

```bash
codex plugin marketplace add r13v/peer
codex plugin add peer@r13v-codex
```

Codex does not run a plugin's hooks until you trust them: open `/hooks` in Codex and trust the peer hooks, again after each update of the plugin. Until then, the skill falls back to `peer wait`.

If you installed the agent skill for Codex before, remove it, so that only the plugin's `peer` skill is left:

```bash
npx skills remove peer -g -a codex
```

The plugin's version is the CLI's: each release of `peer` sets it, so `codex plugin marketplace upgrade` brings the plugin that goes with the new CLI.

To install `peer` itself, see the [main README](../README.md).
