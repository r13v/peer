---
name: peer
description: Pair with another local coding agent on the same Git checkout. Use when the user asks two agents to discuss, implement, or review one task together.
---

# Peer

The user usually invokes this skill with only a task, for example `/peer add CSV export to the reports page`. Work out your role without asking:

1. Your participant name is your app: `claude` in Claude Code, `codex` in Codex, `copilot` in GitHub Copilot. Use a different name only if the user gives one.
2. Run `peer status`. If an active session names you as `writer` or `reader`, that is your role. If an active session names two other participants, stop and ask the user.
3. Otherwise you are the writer. The reader is the agent the user names; by default it is `codex` when you are `claude`, and `claude` when you are not. Run `peer start --writer YOUR_NAME --reader READER`, adding `--open-reader READER` when the reader is `codex` or `claude`. That opens a new reader chat in the checkout with its prompt filled in; tell the user to press Enter there. For any other reader, ask the user to open its chat in this checkout and send: `Use the peer skill. You are participant READER, the reader in YOUR_NAME's peer session in this checkout.` An ended session can be replaced this way for a new task.

Then load your role's instructions from the installed CLI and follow them: `peer skills writer` or `peer skills reader`. The writer sends the task to the reader through `peer`.

If `peer` is unavailable, tell the user the CLI needs installation. Do not improvise from this short entrypoint; the CLI serves the workflow matching its version. Messages written only in your chat do not reach the other agent.
