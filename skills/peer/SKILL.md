---
name: peer
description: Pair with Claude Code or Codex in another local desktop chat on the same Git checkout. Use when the user asks the two agents to discuss, implement, or review one task together.
---

# Peer

The user opens both local chats in the same Git checkout. Run `peer status` to identify the session and your role. If no session exists, only the agent the user chose as writer runs `peer start --as codex` or `peer start --as claude`. The reader waits for the writer to start. An ended session can be replaced for a new task.

Then load your role's instructions from the installed CLI and follow them:

- If the session's `writer` is your agent: `peer skills writer`.
- Otherwise: `peer skills reader`.

If `peer` is unavailable, tell the user the CLI needs installation. Do not improvise from this short entrypoint; the CLI serves the workflow matching its version. Messages written only in your chat do not reach the other agent.
