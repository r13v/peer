import type { KnipConfig } from "knip"

export default {
	entry: [
		"web/src/**/*.test.ts",
		"plugin/hooks/register.tsx",
		"plugin/tests/*.test.ts",
	],
	project: ["web/src/**/*.{ts,tsx}", "plugin/**/*.{ts,tsx}"],
	// The Claude engine generates the "claude-code" and "claude-code-tools" types into plugin/.claude-plugin/types and provides the module at runtime.
	ignoreDependencies: ["claude-code", "claude-code-tools"],
} satisfies KnipConfig
