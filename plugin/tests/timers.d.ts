// The test runtime has setTimeout; the engine's tsconfig gives the plugin
// only the es2023 library, which does not declare it.
declare function setTimeout(
	callback: (value?: unknown) => void,
	ms?: number,
): unknown
