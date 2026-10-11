// Node sizes are measured only with the bundled font loaded, so every tab measures the same.
export async function loadFonts(): Promise<void> {
	await Promise.all([
		document.fonts.load("400 14px Inter"),
		document.fonts.load("600 14px Inter"),
	])
	await document.fonts.ready
}

// False while a font face, such as another unicode-range subset, is still loading.
export function fontsSettled(): boolean {
	return document.fonts.status === "loaded"
}
