# Peer flow

The user usually invokes the peer skill with only a task, for example `/peer add CSV export to the reports page`. Work out your role without asking:

1. Your participant name is your app: `claude` in Claude Code, `codex` in Codex, `copilot` in GitHub Copilot. Use a different name only if the user gives one.
2. If your prompt names a peer room, you are the participant it names in that room; use its ID in every command.
3. Otherwise you are the writer. The reader is the agent the user names; by default it is `codex` when you are `claude`, and `claude` when you are not. Name the room after the task in 2–4 lowercase words joined by `-`, such as `csv-export`, and run `peer start ROOM --writer YOUR_NAME --reader READER`. It prints the room as JSON; its `id` is ROOM, or ROOM-2 and so on if the name was taken, and is the ID for every later command. Other rooms may be active in the same checkout; they do not affect yours. When the reader is `codex` or `claude`, this starts it headless in the checkout; tell the user they can watch it by running `peer` and pressing Tab in the session. Add `--headed` only when the user asks to see the reader's chat: if its desktop app is open, a new chat opens there with the prompt filled in and the user must press Enter; otherwise it still runs headless. For any other reader, ask the user to open its chat in this checkout and send: `Use the peer skill. You are participant READER, the reader in YOUR_NAME's peer room ID in this checkout.` Start a new room for each new task.

Then load your role's instructions and follow them: `peer skills writer` or `peer skills reader`. The writer sends the task to the reader through `peer`. Messages written only in your chat do not reach the other agent.
