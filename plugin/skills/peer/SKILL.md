---
name: peer
description: "Work with other local coding agents on one task in this Git checkout through peer rooms: one main agent leads and edits, members discuss and review, and workers edit their part. Use when the user asks agents to discuss, implement, review or split a task together, or to join a peer room."
---

Run `peer skills flow` and follow it.

Apply the exception below only to a room that a note from the peer plugin in this chat names, saying that the plugin reads the room for main. Otherwise use the CLI instructions unchanged.

When you are main of such a room, the plugin delivers room messages into this chat as a turn of their own, once this session is free. This replaces every `peer wait` instruction in `peer skills main`: do not run `peer wait` as main, neither in the background nor with `--timeout 0`. Read every delivered message, including the last messages when a room ends, and stop working on a room once it has ended. Keep the other main instructions, such as discussing before you edit, asking for review and running `peer end`.

When you are a member or a worker, follow `peer skills member` or `peer skills worker` unchanged: the plugin delivers messages only to main, so receive your own messages with `peer wait`.
