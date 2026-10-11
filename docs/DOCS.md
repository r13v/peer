# Documentation rules

The documentation describes the current design and helps you change it without reading all the code. Language: English, except where a document states otherwise. Write for clarity: active voice, short sentences.

## One owner per topic

Each topic has exactly one owner document. Other documents link to it in one line and do not retell it.

| Document | Owns |
| --- | --- |
| [README.md](../README.md) | For humans: installation, usage, CLI commands and flags, environment variables, how it works, the store layout |
| [AGENTS.md](../AGENTS.md) | Agent working rules, check commands, the "what to read before what" map, review invariants |
| [plugin/README.md](../plugin/README.md) | The Claude Code plugin: installation, behavior, development |
| [codex-plugin/README.md](../codex-plugin/README.md) | The Codex plugin: installation, behavior |
| [CODESTYLE.md](CODESTYLE.md) | Code rules for Go and TypeScript, folder structure |
| [BOARD.md](BOARD.md) | Board design: model, operations, storage, server, SPA, plugin preview, limits |
| [BOARD_API.md](BOARD_API.md) | Board wire contract: board file, packs, CLI output, HTTP |
| [BOARD_PROPOSAL.md](BOARD_PROPOSAL.md) | The original board proposal (Russian), kept for reference; [BOARD.md](BOARD.md) owns the design |
| [instructions/](../instructions/) | Agent-facing texts that `peer skills` prints: flow, main, member, worker |
| DOCS.md | These rules |

## Rules

1. **Link instead of copy.** If you need a fact from another topic, link to the owner's section. Numbers and limits live in one place: in the topic owner or in the code.
2. **Current state only.** Do not write "was", "used to", "previously", "migrated from", "as before", "no longer", "(Done)", "Implemented", run and ticket dates, commit hashes or results of past runs. Support for old data is a current contract, not history.
3. **Plans only in proposals.** A proposal states the future as a plan. Do not describe unimplemented items as working in README.md, AGENTS.md or `instructions/`.
4. **AGENTS.md stays minimal:** rules, commands, the reading map and invariants. File and module contracts belong to their owners.
5. **Paths exist and have no line numbers.** Give a file or folder and check that it exists. Line numbers become stale after each edit.
6. **Contracts and reasons instead of retelling code.** Describe what a module guarantees and why. The code shows how it does this.
7. **The document changes with the code.** A contract change updates the owner document in the same change. A change of a command that agents use also updates `instructions/`.

## Before you commit a documentation edit

- Paths and links exist; anchors resolve.
- No history: `grep -nE 'previously|used to|was changed|migrated|no longer|Done|Implemented|20[0-9]{2}-[0-9]{2}-[0-9]{2}' AGENTS.md README.md docs/*.md plugin/README.md codex-plugin/README.md instructions/*.md` (except DOCS.md itself).
- No duplicates: grep all documents for the topic and keep the text only in the owner.
- `git diff --check`.
