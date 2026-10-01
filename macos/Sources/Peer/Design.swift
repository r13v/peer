import AppKit
import SwiftUI

/// Avatar marks a participant with its role's color and its app's symbol.
struct Avatar: View {
    var role: String
    var agent: String?
    var size: CGFloat = 32

    var body: some View {
        let color = roleColor(role)
        RoundedRectangle(cornerRadius: size * 0.3, style: .continuous)
            .fill(color.gradient)
            .frame(width: size, height: size)
            .overlay {
                Image(systemName: symbol)
                    .font(.system(size: size * 0.45, weight: .semibold))
                    .foregroundStyle(.white)
            }
            .shadow(color: color.opacity(0.25), radius: 2, y: 1)
    }

    private var symbol: String {
        switch (role, agent) {
        case ("user", _): "person.fill"
        case ("peer", _): "gearshape.fill"
        case (_, "claude"): "sparkle"
        case (_, "codex"): "chevron.left.forwardslash.chevron.right"
        case ("writer", _): "pencil"
        default: "person.crop.circle"
        }
    }
}

struct StatusDot: View {
    var color: Color
    var glow: Bool
    var size: CGFloat = 8

    var body: some View {
        Circle()
            .fill(color)
            .frame(width: size, height: size)
            .shadow(color: glow ? color.opacity(0.6) : .clear, radius: 3)
    }
}

/// Pill is a small colored capsule for a state, such as Waiting.
struct Pill: View {
    var text: String
    var color: Color

    var body: some View {
        Text(text)
            .font(.caption.weight(.semibold))
            .monospacedDigit()
            .foregroundStyle(color)
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(color.opacity(0.13), in: Capsule())
            .fixedSize()
    }
}

struct SectionTitle: View {
    var title: String
    var count: Int?

    init(_ title: String, count: Int? = nil) {
        self.title = title
        self.count = count
    }

    var body: some View {
        HStack(spacing: 6) {
            Text(title.uppercased())
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
                .tracking(0.6)
            if let count {
                Text("\(count)").font(.caption).foregroundStyle(.tertiary)
            }
        }
    }
}

private let palette: [Color] = [.purple, .pink, .indigo, .mint, .blue, .brown]

/// roleColor gives writer, user and peer fixed colors, and every other
/// role a stable one of the palette.
func roleColor(_ role: String) -> Color {
    switch role {
    case "writer": .teal
    case "user": .orange
    case "peer": .gray
    default: palette[Int(role.unicodeScalars.reduce(0) { ($0 &* 31 &+ $1.value) % 9973 }) % palette.count]
    }
}

func stateColor(_ state: String) -> Color {
    switch state {
    case "waiting": .green
    case "busy": .orange
    case "exited": .red
    default: .secondary
    }
}

func copy(_ text: String) {
    NSPasteboard.general.clearContents()
    NSPasteboard.general.setString(text, forType: .string)
}

func reveal(_ path: String) {
    NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: path)])
}

/// abbreviate writes a path under the home directory with ~.
func abbreviate(_ path: String) -> String {
    (path as NSString).abbreviatingWithTildeInPath
}

func date(_ iso: String) -> Date? {
    (try? Date.ISO8601FormatStyle(includingFractionalSeconds: true).parse(iso)) ?? (try? Date.ISO8601FormatStyle().parse(iso))
}

/// time is a date and time, without the date for today.
func time(_ iso: String) -> String {
    guard let date = date(iso) else { return "" }
    return date.formatted(date: Calendar.current.isDateInToday(date) ? .omitted : .abbreviated, time: .shortened)
}

/// clock is the time of day, as messages show it.
func clock(_ iso: String) -> String {
    date(iso)?.formatted(date: .omitted, time: .shortened) ?? ""
}

func dayLabel(_ iso: String) -> String {
    guard let date = date(iso) else { return "" }
    if Calendar.current.isDateInToday(date) { return "Today" }
    if Calendar.current.isDateInYesterday(date) { return "Yesterday" }
    return date.formatted(.dateTime.weekday(.wide).day().month(.wide))
}

func duration(_ iso: String, until end: Date) -> String {
    guard let since = date(iso) else { return "" }
    let s = max(0, Int(end.timeIntervalSince(since)))
    return s < 60 ? "\(s)s" : s < 3600 ? "\(s / 60)m" : "\(s / 3600)h \(s % 3600 / 60)m"
}

/// short is how long ago iso was, in a few characters, as sidebars show it.
func short(_ iso: String, now: Date) -> String {
    guard let d = date(iso) else { return "" }
    let s = max(0, Int(now.timeIntervalSince(d)))
    switch s {
    case ..<60: return "now"
    case ..<3600: return "\(s / 60)m"
    case ..<86400: return "\(s / 3600)h"
    default: return d.formatted(.dateTime.day().month(.abbreviated))
    }
}
