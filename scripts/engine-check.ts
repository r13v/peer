// engine-check checks the Claude Code plugin against the installed Claude
// Code once per version and plugin: the plugin's typecheck against that
// version's types, which typecheck:plugin has it write, then the plugin's
// tests in the CLI's kit. A stamp in the user's cache keeps the plugin
// trees that passed under this version, one line each, so worktrees with
// different plugins do not check again in turn, and a new commit of the
// plugin is checked again under the same CLI; --force checks again. It prints
// everything to stdout: a SessionStart hook puts only stdout into the
// session.
import { spawnSync } from "node:child_process"
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs"
import { homedir } from "node:os"
import { dirname, join } from "node:path"

// A SessionStart hook runs this, so a Claude Code session started by a step
// must not run it again.
if (process.env.PEER_ENGINE_CHECK) process.exit(0)
process.env.PEER_ENGINE_CHECK = "1"

const root = join(import.meta.dirname, "..")
const stampFile = join(homedir(), ".cache/peer/engine-checked")

const cli = spawnSync("claude", ["--version"], { encoding: "utf8" })
	.stdout?.trim()
	.split(" ")[0]
if (!cli) {
	console.log("engine-check: cannot read claude --version")
	process.exit(1)
}

// The tree of the committed plugin: uncommitted edits are the pre-commit
// typecheck's.
const plugin = spawnSync("git", ["rev-parse", "HEAD:plugin"], {
	cwd: root,
	encoding: "utf8",
}).stdout?.trim()
const pair = `cli ${cli}, plugin ${plugin || "?"}`
let passed: string[] = []
try {
	// Pairs of an older CLI are dropped, so the stamp does not grow.
	passed = readFileSync(stampFile, "utf8")
		.split("\n")
		.filter((line) => line.startsWith(`cli ${cli}, `))
} catch {
	// no stamp: never checked on this machine
}
if (passed.includes(pair) && !process.argv.includes("--force")) process.exit(0)

// A checkout without dependencies has no tsc to check with.
if (!existsSync(join(root, "node_modules"))) {
	console.log("engine-check: skipped, no node_modules (npm ci)")
	process.exit(0)
}

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
writeFileSync(
	stampFile,
	`${[...passed.filter((line) => line !== pair), pair].join("\n")}\n`,
)
console.log(`checked against Claude Code ${cli}`)
