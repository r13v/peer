---
name: peer
description: Pair with another local coding agent on the same Git checkout. Use when the user asks two agents to discuss, implement, or review one task together.
---

# Peer

The user opens a local chat in the Git checkout for each agent, or only the writer's chat when the writer opens the reader chat, and gives each agent a distinct participant name (for example, `claude` and `copilot`). Run `peer status` to identify the session and your role. If no session exists, only the chosen writer runs `peer start --writer YOUR_NAME --reader OTHER_NAME`. If the reader chat is not open yet and the reader is Codex Desktop or Claude Desktop, add `--open-reader codex` or `--open-reader claude`; this opens a new chat in the checkout with the reader prompt filled in, and the user presses Enter there. The reader waits for the writer to start. An ended session can be replaced for a new task.

Then load your role's instructions from the installed CLI and follow them:

- If the session's `writer` is your participant name: `peer skills writer`.
- If the session's `reader` is your participant name: `peer skills reader`.
- If neither matches, stop and ask the user to identify your participant name.

If `peer` is unavailable, tell the user the CLI needs installation. Do not improvise from this short entrypoint; the CLI serves the workflow matching its version. Messages written only in your chat do not reach the other agent.
