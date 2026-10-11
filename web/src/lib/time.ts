const units: [Intl.RelativeTimeFormatUnit, number][] = [
	["year", 365 * 24 * 3600],
	["month", 30 * 24 * 3600],
	["week", 7 * 24 * 3600],
	["day", 24 * 3600],
	["hour", 3600],
	["minute", 60],
]

const relative = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" })
const dateTime = new Intl.DateTimeFormat(undefined, {
	month: "short",
	day: "numeric",
	hour: "2-digit",
	minute: "2-digit",
})
const timeOnly = new Intl.DateTimeFormat(undefined, {
	hour: "2-digit",
	minute: "2-digit",
})

// "5 minutes ago", "yesterday"; under a minute is "now".
export function timeAgo(iso: string, now = Date.now()): string {
	const seconds = (new Date(iso).getTime() - now) / 1000
	for (const [unit, size] of units) {
		if (Math.abs(seconds) >= size) {
			return relative.format(Math.round(seconds / size), unit)
		}
	}

	return relative.format(0, "second")
}

export function fullTime(iso: string): string {
	return new Date(iso).toLocaleString()
}

// "Oct 11, 10:26 – 10:38": the end drops the date when it falls on the same day.
export function timeRange(startIso: string, endIso?: string): string {
	const start = new Date(startIso)
	if (!endIso) {
		return `since ${dateTime.format(start)}`
	}
	const end = new Date(endIso)
	const sameDay = start.toDateString() === end.toDateString()

	return `${dateTime.format(start)} – ${(sameDay ? timeOnly : dateTime).format(end)}`
}
