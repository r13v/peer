---
name: peer
description: "Work with other local coding agents on one task in this Git checkout through peer rooms: one main agent leads and edits, members discuss and review, and workers edit their part. Use when the user asks agents to discuss, implement, review or split a task together, or to join a peer room."
---

Run `peer skills flow` and follow it. Run `peer start` as a command of its own, not joined with other commands, so the plugin sees it.

Apply the exception below only to a room that a note from the peer plugin in this session names, saying that the plugin delivers its messages. Otherwise use the CLI instructions unchanged.

When you are main of such a room, the plugin puts each batch of the room's messages into this session as a turn of its own. This replaces every `peer wait` instruction in `peer skills main` for that room: do not run `peer wait` for it, neither in the background nor with `--timeout 0`; peer refuses it while the plugin delivers. Read every delivered batch, including the last messages when the room ends, and stop working on the room once it has ended. A batch can come twice; its batch ID shows a repeat. Keep the other main instructions, such as discussing before you edit, asking for review and running `peer end`. If the plugin says that it stopped delivering a room, run `peer wait` for that room as `peer skills main` says.

If no such note comes after `peer start`, for example because Copilot runs without `--experimental`, run `peer wait` as `peer skills main` says.

When you are a member or a worker, follow `peer skills member` or `peer skills worker` unchanged, and receive your own messages with `peer wait`.
