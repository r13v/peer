// typecheck-plugin checks the Claude Code plugin against the types of the
// installed Claude Code. Claude Code writes them beside the plugin, in
// plugin/.claude-plugin/types, only when it loads the plugin in an
// interactive session, and its first line names the version that wrote
// them. When that version is not `claude --version`, this script loads the
// plugin once in a pseudo-terminal, as a session that is not logged in,
// with a HOME of its own, so the person's settings and sessions stay out of
// it, and waits for the new types. Without claude it checks the types there
// are, or skips.
import { spawn, spawnSync } from "node:child_process"
import {
	existsSync,
	mkdtempSync,
	readFileSync,
	realpathSync,
	rmSync,
	writeFileSync,
} from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { setTimeout as sleep } from "node:timers/promises"

const root = join(import.meta.dirname, "..")
const typesDir = join(root, "plugin/.claude-plugin/types")
const typesFile = join(typesDir, "claude-code/index.d.ts")

function writtenBy() {
	try {
		const first = readFileSync(typesFile, "utf8").split("\n", 1)[0] ?? ""
		return /^\/\/ Written by Claude Code (\S+)\./.exec(first)?.[1] ?? ""
	} catch {
		return ""
	}
}

// load starts claude on the plugin in a pseudo-terminal, which script(1)
// gives it, until the types of version cli are there or a minute passes.
async function load(cli: string) {
	const home = mkdtempSync(join(tmpdir(), "peer-types-"))
	// A session that has seen its first-run screens and trusts the checkout
	// loads the plugin at once.
	writeFileSync(
		join(home, ".claude.json"),
		JSON.stringify({
			hasCompletedOnboarding: true,
			theme: "dark",
			projects: { [realpathSync(root)]: { hasTrustDialogAccepted: true } },
		}),
	)
	// A variable of the session that runs this script would make the new
	// session its child. PEER_ENGINE_CHECK keeps a SessionStart hook of the
	// checkout from running engine-check in it.
	const env: Record<string, string | undefined> = {
		...process.env,
		HOME: home,
		PEER_ENGINE_CHECK: "1",
	}
	for (const key of Object.keys(env))
		if (key === "CLAUDECODE" || key.startsWith("CLAUDE_CODE_")) delete env[key]
	// script(1) runs claude in a session of its own, so claude's pid goes to
	// a file to stop it by. script(1) reads a pipe that stays open, from
	// sleep: it ends at the end of its input, and macOS's refuses Node's own
	// pipe, a socket.
	const pidFile = join(home, "claude.pid")
	const claude = `sh -c 'echo $$ > "$0"; exec claude --plugin-dir plugin' ${pidFile}`
	const script =
		process.platform === "darwin"
			? `script -q /dev/null ${claude}`
			: `script -qec "${claude.replaceAll('"', '\\"')}" /dev/null`
	const child = spawn("sh", ["-c", `sleep 120 | ${script}`], {
		cwd: root,
		env,
		stdio: "ignore",
		detached: true,
	})
	try {
		for (let i = 0; i < 240 && writtenBy() !== cli; i++) await sleep(250)
		// The engine writes several files; let it finish the rest.
		await sleep(1000)
	} finally {
		// TERM lets claude stop what the plugin started; KILL ends the rest.
		const pid = Number(readFileSync(pidFile, { encoding: "utf8", flag: "a+" }))
		for (const signal of ["SIGTERM", "SIGKILL"] as const) {
			for (const target of [pid, -(child.pid ?? 0)]) {
				try {
					if (target) process.kill(target, signal)
				} catch {
					// it has exited
				}
			}
			await sleep(2000)
		}
		rmSync(home, { recursive: true, force: true })
	}
}

const cli = spawnSync("claude", ["--version"], { encoding: "utf8" })
	.stdout?.trim()
	.split(" ")[0]
if (!cli && !existsSync(typesFile)) {
	console.error(
		"typecheck:plugin SKIPPED: no claude on PATH to write plugin/.claude-plugin/types",
	)
	process.exit(0)
}
if (cli && writtenBy() !== cli) {
	await load(cli)
	if (writtenBy() !== cli) {
		console.error(
			`typecheck:plugin: Claude Code ${cli} did not write plugin/.claude-plugin/types within a minute; run claude --plugin-dir plugin in a terminal`,
		)
		process.exit(1)
	}
}
const tsc = spawnSync(
	"npx",
	["--no-install", "tsc", "-p", "plugin/tsconfig.json"],
	{
		cwd: root,
		stdio: "inherit",
	},
)
if (tsc.status !== 0)
	console.error(
		`typecheck:plugin failed against Claude Code ${writtenBy()} types`,
	)
process.exit(tsc.status ?? 1)
