// Board keys are base64url without padding of the UTF-8 absolute board path (docs/BOARD_API.md).
export function base64url(text: string): string {
	const bytes = new TextEncoder().encode(text)
	let binary = ""
	for (const byte of bytes) {
		binary += String.fromCharCode(byte)
	}
	const encoded = btoa(binary)
		.replaceAll("+", "-")
		.replaceAll("/", "_")
		.replace(/=+$/, "")

	return encoded
}
