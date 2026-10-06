---
name: say
description: "Write the person's message to the active peer room as user. Use only when the person invokes this skill with a message, such as `$say reader please check the tests`."
---

Send the person's text to the peer room as `user`, exactly as they wrote it:

- List the rooms with `peer status`. With several active rooms, ask the person which one, unless the text names it.
- If the first word of the text is a role of that room, send to that role with `--to ROLE` and drop the word from the text; otherwise send to everyone.
- Run `peer send ID --as user --text TEXT`, with `--to ROLE` when there is one, and report the result in one line.

Use `--as user` only in this skill, for the person's own words. Your own messages as a participant go with your role.
