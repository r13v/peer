# Peer flow

The user usually invokes the peer skill with only a task, for example `/peer add CSV export to the reports page`. Work out your role without asking:

1. Your participant name is your app: `claude` in Claude Code, `codex` in Codex, `copilot` in GitHub Copilot. Use a different name only if the user gives one.
2. Run `peer status`. If an active session names you as `writer` or `reader`, that is your role. If an active session names two other participants, stop and ask the user.
3. Otherwise you are the writer. The reader is the agent the user names; by default it is `codex` when you are `claude`, and `claude` when you are not. Run `peer start --writer YOUR_NAME --reader READER`. When the reader is `codex` or `claude`, this starts it headless in the checkout; tell the user they can watch it by running `peer` and pressing Tab in the session. Add `--headed` only when the user asks to see the reader's chat: if its desktop app is open, a new chat opens there with the prompt filled in and the user must press Enter; otherwise it still runs headless. For any other reader, ask the user to open its chat in this checkout and send: `Use the peer skill. You are participant READER, the reader in YOUR_NAME's peer session in this checkout.` An ended session can be replaced this way for a new task.

Then load your role's instructions and follow them: `peer skills writer` or `peer skills reader`. The writer sends the task to the reader through `peer`. Messages written only in your chat do not reach the other agent.
