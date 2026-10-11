import { writeFileSync } from "node:fs"
import { fileURLToPath } from "node:url"
import react from "@vitejs/plugin-react"
import { defineConfig, type Plugin } from "vite"

const root = fileURLToPath(new URL(".", import.meta.url))
const outDir = fileURLToPath(new URL("dist", import.meta.url))
const devServer = "http://127.0.0.1:4747"

// emptyOutDir removes dist/.keep, which `go build` needs for `//go:embed all:dist`.
function keepDist(): Plugin {
	return {
		name: "peer-keep-dist",
		apply: "build",
		closeBundle() {
			writeFileSync(`${outDir}/.keep`, "")
		},
	}
}

export default defineConfig(({ command }) => ({
	root,
	// The Go server serves the build under /_app/; the dev server serves it at the root.
	base: command === "build" ? "/_app/" : "/",
	plugins: [react(), keepDist()],
	build: {
		outDir,
		emptyOutDir: true,
	},
	server: {
		proxy: {
			"/api": devServer,
			"^/b/[^/]+/assets/": devServer,
		},
	},
}))
