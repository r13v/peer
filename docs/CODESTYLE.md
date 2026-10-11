# Code style

Priorities: readability, simplicity, maintainability. The rules apply to Go and TypeScript. Formatters and linters check the form; review checks the other rules.

- **Go:** `golangci-lint` (`.golangci.yml`) runs the linters and the `gofmt` and `goimports` formatters. Run `make fmt` and `make lint`.
- **TypeScript:** Biome (`biome.json`) checks formatting and lint. Run `npm run check:fix`.

## Readability

1. **Separate steps with a blank line.** A guard clause, a computation and a result are separate paragraphs. Put a blank line before the final `return`, except in a block with a single statement.
2. **Name the result.** Assign a multi-line expression to a variable with a domain name, then return the variable.
3. **Keep conditional expressions short.** In TypeScript, a ternary has a variable or a call as its condition and simple values as its branches; do not nest ternaries. Go has no ternary: use `if` with an early return or a `switch`.
4. **Use early return.** Remove unsuitable cases before the main scenario. In Go, handle an error right after the call and return; keep the main scenario at the lowest indentation.
5. **Destructure a repeated path.** In TypeScript, instead of `room.state.a`, `room.state.b`, write `const { a, b } = room.state`. In Go, assign the repeated path to a local variable.

## Simplicity

6. **Access data directly.** Take the last N items with a slice (`s[len(s)-n:]` in Go, `slice(-n)` in TypeScript) and the last item with an index (`s[len(s)-1]`, `at(-1)`). Do not compute an array of indexes to iterate over them.
7. **Do not nest callbacks deeper than one level.** Move a callback or a closure with several steps to a named function.
8. **Do not create abstractions for the future.** Extract a function, a type or a constant for reuse or to name a non-obvious step. Do not add interfaces, options or wrappers for future flexibility.

## Maintainability

9. **Name complex conditions.** Move a check with several parts to a function or a variable with a domain name, such as `isOutside` or `canDeliver`.
10. **Name non-obvious literals.** A limit, a timeout, a window and a status code get a constant near their use.
11. **Explain the reason in a comment.** The code and the names must make the action clear; see [Comments](#comments).
12. **Keep shared helpers in one place.** A helper of one file or package stays there. In the SPA, a helper that several modules use goes to `web/src/lib/<topic>.ts`. Do not create copies.
13. **Update dependencies deliberately.** Choose current stable versions, check compatibility and update `go.sum` or `package-lock.json`. Do not update the whole stack as part of an unrelated task.

### Go

- Wrap an error with context and `%w`: `fmt.Errorf("read board %s: %w", id, err)`. Compare errors with `errors.Is` and `errors.As`.
- Do not use named results unless a deferred function must change them or they document two results of the same type.
- Write a table-driven test only when the cases share one check and the table makes the intent clearer. Otherwise write a separate test per behavior.

### TypeScript

- Give the extension of a relative import: `.ts` or `.tsx`. Vite resolves the source files; `allowImportingTsExtensions` lets `tsc` accept them.
- Name files in kebab-case. Biome checks it.
- Use `type` imports for types only.

## Code structure

- **Root, Go `package main`:** the CLI (`main.go`), the TUI (`tui.go`), the room store (`store.go`), agent start-up (`agent.go`), message forwarding (`forward.go`), member logs (`memberlog.go`), the Codex hook (`codexhook.go`), the `peer board` commands (`boardcmd.go`) and the board server start-up and room provider for the TUI (`boardserver.go`). Tests sit next to the code as `*_test.go`.
- **`internal/board`** (board feature): the board model, validation and `apply` of an operation batch.
- **`internal/boardhttp`** (board feature): the local HTTP server, the board API and SSE.
- **`web/`** (board feature): the SPA on Vite, React and `@xyflow/react`, and the Go package `web` (`web/embed.go`) that embeds the build. Source code is in `web/src`; shared helpers are in `web/src/lib`. The build goes to `web/dist`, which the Go binary embeds.
- **`plugin/`:** the Claude Code plugin, a TypeScript mod (`plugin/hooks/register.tsx`, tests in `plugin/tests`). It has its own `tsconfig.json`. See [plugin/README.md](../plugin/README.md).
- **`codex-plugin/`:** the Codex plugin. See [codex-plugin/README.md](../codex-plugin/README.md).
- **`instructions/`:** the agent-facing texts that `peer skills` prints; the binary embeds them.
- **`skills/`:** the agent skill that `npx skills add` installs.

## Comments

Check all comments and doc comments within the scope of the task. Do not extend the cleanup to adjacent code, and do not add comments where there were none.

Keep information that the code, names and types do not show:

- the reason for a decision, an external constraint or a workaround with a link to an issue or a specification;
- an invariant, an order of actions, concurrency, locking or safety conditions;
- a unit of measurement, a format or an API contract;
- a current TODO with an owner or an issue.

Remove retelling of the code or the signature, history and development steps, commented-out code, empty separators, and done or ownerless TODOs.

Describe the current state briefly; keep conditions, limits and links. For example: "Held on a separate file: the rename replaces board.json." Support for old data is a current contract, not change history.

Keep licenses, shebangs, directives (`//go:embed`, `//nolint`, `biome-ignore`, `@ts-expect-error`), machine annotations and Go doc comments of exported names. You can shorten their content; do not change the syntax of directives.

After the cleanup, check that the executable code did not change: run `make lint` and `make test` for Go and `npm run verify` for TypeScript.

## Example

A guard clause, a named condition and direct access to the last items:

```go
const recentLimit = 16

func recentFor(msgs []message, role string) []message {
	if len(msgs) == 0 {
		return nil
	}

	var forRole []message
	for _, m := range msgs {
		if isAddressedTo(m, role) {
			forRole = append(forRole, m)
		}
	}
	recent := forRole[max(0, len(forRole)-recentLimit):]

	return recent
}
```

`isAddressedTo` checks the recipient: the role itself or everyone.
