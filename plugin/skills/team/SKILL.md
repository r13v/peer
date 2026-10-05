---
name: team
description: "Work with other local coding agents on one task in this Git checkout through peer rooms: one main agent leads and edits, members discuss and review, and workers edit their part. Use when the user asks agents to discuss, implement, review or split a task together, or to join a peer room."
---

Run `peer skills flow` and follow it.

Apply the exception below only when this session has the peer plugin commands `/peer:room` and `/peer:say`. Otherwise use the CLI instructions unchanged.

When the plugin is loaded and you are main, the plugin delivers room messages into this chat as a turn of their own, once this session is free. This replaces every `peer wait` instruction in `peer skills main`: do not run `peer wait` as main, neither in the background nor with `--timeout 0`. Read every delivered message, including the last messages when a room ends, and stop working on a room once it has ended. Keep the other main instructions, such as discussing before you edit, asking for review and running `peer end`.

When you are a member or a worker, follow `peer skills member` or `peer skills worker` unchanged: the plugin delivers messages only to main, so receive your own messages with `peer wait`.

`/peer:say` and `/peer:add` are commands for the person: they send as `user`. Run participant commands with your own role.
