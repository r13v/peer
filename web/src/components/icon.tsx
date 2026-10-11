// Stroke icons on a 24-unit grid; they take the color of the surrounding text.
const paths = {
	plus: "M12 5v14M5 12h14",
	card: "M4 5h16v14H4zM8 10h8M8 14h5",
	frame: "M4 8V4h4M16 4h4v4M20 16v4h-4M8 20H4v-4",
	undo: "M9 14 4 9l5-5M4 9h11a5 5 0 0 1 0 10h-3",
	redo: "m15 14 5-5-5-5M20 9H9a5 5 0 0 0 0 10h3",
	play: "M7 4v16l13-8z",
	message: "M21 12a8 8 0 0 1-11.6 7.1L4 20l1-4.6A8 8 0 1 1 21 12z",
	search: "M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14zM20 20l-4-4",
	back: "m15 18-6-6 6-6",
	next: "m9 18 6-6-6-6",
	refresh: "M20 11a8 8 0 0 0-14.8-4M4 4v4h4M4 13a8 8 0 0 0 14.8 4M20 20v-4h-4",
	check: "M20 6 9 17l-5-5",
	reopen: "M3 12a9 9 0 1 0 3-6.7L3 8M3 3v5h5",
	close: "M18 6 6 18M6 6l12 12",
	arrange: "M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z",
	board: "M3 3h18v18H3zM3 9h18M9 21V9",
	send: "M5 12h14M13 6l6 6-6 6",
}

export type IconName = keyof typeof paths

export function Icon({ name, size = 16 }: { name: IconName; size?: number }) {
	return (
		<svg
			className="icon"
			width={size}
			height={size}
			viewBox="0 0 24 24"
			fill="none"
			stroke="currentColor"
			strokeWidth={2}
			strokeLinecap="round"
			strokeLinejoin="round"
			aria-hidden="true"
		>
			<path d={paths[name]} />
		</svg>
	)
}
