// engine-check checks the Claude Code plugin against the installed Claude
// Code once per version: the plugin's typecheck against that version's
// types, which typecheck:plugin has it write, then the plugin's tests in the
// CLI's kit. A stamp in the user's cache keeps the version it last passed;
// --force checks again. It prints everything to stdout: a SessionStart hook
// puts only stdout into the session.
import { spawnSync } from "node:child_process"
import { mkdirSync, readFileSync, writeFileSync } from "node:fs"
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

const pair = `cli ${cli}`
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
console.log(`checked against Claude Code ${cli}`)
