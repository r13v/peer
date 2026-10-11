// engine-check checks the Claude Code plugin against the installed Claude
// Code once per engine: the plugin's typecheck against the types the engine
// wrote, then the plugin's tests in the CLI's kit. A stamp in the user's
// cache keeps the pair of versions it last passed; --force checks again.
// It prints everything to stdout: a SessionStart hook puts only stdout into
// the session.
import { spawnSync } from "node:child_process"
import { mkdirSync, readFileSync, writeFileSync } from "node:fs"
import { homedir } from "node:os"
import { dirname, join } from "node:path"

// A SessionStart hook runs this, so a Claude Code session started by a step
// must not run it again.
if (process.env.PEER_ENGINE_CHECK) process.exit(0)
process.env.PEER_ENGINE_CHECK = "1"

const root = join(import.meta.dirname, "..")
const typesFile = join(
	root,
	"plugin/.claude-plugin/types/claude-code/index.d.ts",
)
const stampFile = join(homedir(), ".cache/peer/engine-checked")

let firstLine = ""
try {
	firstLine = readFileSync(typesFile, "utf8").split("\n", 1)[0] ?? ""
} catch {
	console.log(
		"engine-check: plugin/.claude-plugin/types is missing; Claude Code writes it when it loads the plugin interactively: run claude --plugin-dir plugin in a terminal, then run this again",
	)
	process.exit(1)
}
const types = /^\/\/ Written by Claude Code (\S+)\./.exec(firstLine)?.[1]
const cli = spawnSync("claude", ["--version"], { encoding: "utf8" })
	.stdout?.trim()
	.split(" ")[0]
if (!types || !cli) {
	console.log(
		`engine-check: cannot read the versions (types: ${types ?? "?"}, cli: ${cli ?? "?"})`,
	)
	process.exit(1)
}

const pair = `types ${types}, cli ${cli}`
let stamp = ""
try {
	stamp = readFileSync(stampFile, "utf8").trim()
} catch {
	// no stamp: never checked on this machine
}
if (stamp === pair && !process.argv.includes("--force")) process.exit(0)

for (const step of ["typecheck:plugin", "test:plugin"]) {
	const run = spawnSync("sh", ["-c", `npm run --silent ${step} 2>&1`], {
		cwd: root,
		encoding: "utf8",
	})
	if (run.status !== 0) {
		console.log(`engine-check: ${step} failed (${pair})`)
		console.log((run.stdout ?? "").trimEnd().split("\n").slice(-30).join("\n"))
		process.exit(1)
	}
}

mkdirSync(dirname(stampFile), { recursive: true })
writeFileSync(stampFile, `${pair}\n`)
console.log(`checked against Claude Code (${pair})`)
if (types !== cli)
	console.log(
		`the typecheck used the types of Claude Code ${types}, not of the CLI ${cli}: run claude --plugin-dir plugin in a terminal so the CLI writes its types, then npm run engine-check`,
	)
