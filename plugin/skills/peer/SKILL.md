---
name: peer
description: "Work with other local coding agents on one task in this Git checkout through peer rooms: one main agent leads and edits, members discuss and review, and workers edit their part. Use when the user asks agents to discuss, implement, review or split a task together, or to join a peer room."
---

Run `peer skills flow` and follow it.

In this session the peer plugin reads the rooms you start or join and delivers their messages into this chat as a turn of their own, once this session is free. Then `peer wait` for that room refuses with "peer delivers room ... into its session". After that refusal the delivered turns replace every `peer wait` instruction in `peer skills main` or `peer skills member`: do not run `peer wait` for that room again, neither in the background nor with `--timeout 0`. Read every delivered message, including the last messages when a room ends, and stop work on the room once it has ended or a delivered turn says that you were kicked. Keep the other instructions, such as discussing before you edit, asking for review and running `peer end`.

When `peer wait` does not refuse, as for a worker, follow the CLI instructions unchanged.
