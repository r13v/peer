import AppKit
import Foundation

/// FileRef is a file that a message or log names, such as main.go:42.
struct FileRef: Hashable, Identifiable {
    var path: String
    var line: Int?
    var column: Int?
    var label: String
    var id: String { label }
    var url: URL { URL(fileURLWithPath: path) }

    /// link opens the file through the app's openURL handler.
    var link: URL {
        var c = URLComponents()
        c.scheme = "peer-file"
        c.path = path
        c.queryItems = [("line", line), ("column", column)].compactMap { name, v in v.map { URLQueryItem(name: name, value: String($0)) } }
        if c.queryItems!.isEmpty { c.queryItems = nil }
        return c.url!
    }

    init?(link url: URL) {
        guard url.scheme == "peer-file" else { return nil }
        let items = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        let value = { (name: String) in items.first { $0.name == name }?.value.flatMap { Int($0) } }
        self.init(path: url.path, line: value("line"), column: value("column"), label: url.lastPathComponent)
    }

    init(path: String, line: Int?, column: Int? = nil, label: String) {
        self.path = path
        self.line = line
        self.column = column
        self.label = label
    }

    /// open opens the file in the editor that Settings names, such as
    /// vscode://file/{path}:{line}, or else in its default app.
    func open() {
        let template = UserDefaults.standard.string(forKey: "editorURL") ?? ""
        if !template.isEmpty {
            let link = template
                .replacingOccurrences(of: "{path}", with: path.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? path)
                .replacingOccurrences(of: "{line}", with: String(line ?? 1))
                .replacingOccurrences(of: "{column}", with: String(column ?? 1))
            if let target = URL(string: link), NSWorkspace.shared.open(target) { return }
        }
        NSWorkspace.shared.open(url)
    }

    func reveal() { NSWorkspace.shared.activateFileViewerSelecting([url]) }
}

/// Markup prepares a message for rendering: it links paths of files that
/// exist in the checkout, as the TUI does, and splits off code blocks that
/// hold a diff, which Textual's highlighter leaves uncolored.
enum Markup {
    /// pathRef matches main.go, ui/src/a.ts:42 or /abs/b.go:3:7, as peer's pathRef.
    private static let pathRef = try! NSRegularExpression(pattern: #"(?:/|\.{1,2}/)?(?:[\w.@-]+/)*[\w@-][\w.@-]*\.[A-Za-z]\w*(?::(\d+))?(?::\d+)?"#)
    /// skipped matches spans to leave as they are: links, URLs and code
    /// spans that are not a path.
    private static let protected = try! NSRegularExpression(pattern: #"\[[^\]\n]*\]\([^)\n]*\)|<[^>\n]+>|[a-z][a-z0-9+.-]*://\S+|`[^`\n]+`"#)

    /// Segment is markdown to render, or the lines of a diff block.
    enum Segment: Hashable {
        case markdown(String)
        case diff([String])
    }

    /// cache keeps segments by checkout and text: a list redraws its rows
    /// on every read, and linking checks the disk for each path.
    @MainActor private static var cache: [String: [Segment]] = [:]

    @MainActor static func segments(_ text: String, repo: String) -> [Segment] {
        let key = repo + "\u{0}" + text
        if let hit = cache[key] { return hit }
        if cache.count > 4000 { cache.removeAll() }
        let out = split(text, repo: repo)
        cache[key] = out
        return out
    }

    private static func split(_ text: String, repo: String) -> [Segment] {
        var out: [Segment] = []
        var markdown: [String] = []
        let flush = {
            if !markdown.isEmpty { out.append(.markdown(render(markdown.joined(separator: "\n"), repo: repo))) }
            markdown = []
        }
        let lines = text.components(separatedBy: "\n")
        var i = 0
        while i < lines.count {
            if let fence = Fence(lines[i]) {
                var end = i + 1
                while end < lines.count, !fence.closes(lines[end]) { end += 1 }
                let body = Array(lines[(i + 1)..<min(end, lines.count)])
                let lang = fence.info
                if lang == "diff" || lang == "patch" || (lang.isEmpty && isDiff(body)) {
                    flush()
                    out.append(.diff(body))
                    i = end + 1
                    continue
                }
                markdown += lines[i...min(end, lines.count - 1)]
                i = end + 1
                continue
            }
            markdown.append(lines[i])
            i += 1
        }
        flush()
        return out
    }

    static func render(_ text: String, repo: String) -> String {
        var out: [String] = []
        let lines = text.components(separatedBy: "\n")
        var i = 0
        while i < lines.count {
            let line = lines[i]
            if let fence = Fence(line) {
                var end = i + 1
                while end < lines.count, !fence.closes(lines[end]) { end += 1 }
                let body = Array(lines[(i + 1)..<min(end, lines.count)])
                out.append(line)
                out += body
                if end < lines.count { out.append(lines[end]) }
                i = end + 1
                continue
            }
            out.append(link(line, repo: repo))
            i += 1
        }
        return out.joined(separator: "\n")
    }

    /// files lists the files that text names and that exist.
    static func files(_ text: String, repo: String) -> [FileRef] {
        var seen = Set<String>()
        let ns = text as NSString
        return pathRef.matches(in: text, range: NSRange(location: 0, length: ns.length)).compactMap { m in
            let ref = ns.substring(with: m.range)
            guard seen.insert(ref).inserted else { return nil }
            return file(ref, repo: repo)
        }
    }

    static func isDiff(_ lines: [String]) -> Bool {
        lines.contains { $0.hasPrefix("diff --git") || $0.hasPrefix("@@ ") }
            || (lines.contains { $0.hasPrefix("--- ") } && lines.contains { $0.hasPrefix("+++ ") })
    }

    private static func file(_ ref: String, repo: String) -> FileRef? {
        let parts = ref.split(separator: ":", omittingEmptySubsequences: false)
        let name = String(parts[0])
        let path = name.hasPrefix("/") ? name : (repo as NSString).appendingPathComponent(name)
        var dir: ObjCBool = false
        guard FileManager.default.fileExists(atPath: path, isDirectory: &dir), !dir.boolValue else { return nil }
        return FileRef(path: (path as NSString).standardizingPath, line: parts.count > 1 ? Int(parts[1]) : nil, column: parts.count > 2 ? Int(parts[2]) : nil, label: ref)
    }

    /// link turns path references in one line outside code blocks into
    /// links, including a code span that holds only a path.
    private static func link(_ line: String, repo: String) -> String {
        let ns = line as NSString
        var out = "", at = 0
        for m in protected.matches(in: line, range: NSRange(location: 0, length: ns.length)) {
            out += linkPaths(ns.substring(with: NSRange(location: at, length: m.range.location - at)), repo: repo)
            let span = ns.substring(with: m.range)
            if span.hasPrefix("`"), let f = file(String(span.dropFirst().dropLast()), repo: repo), whole(String(span.dropFirst().dropLast())) {
                out += "[\(span)](\(f.link.absoluteString))"
            } else {
                out += span
            }
            at = m.range.location + m.range.length
        }
        return out + linkPaths(ns.substring(from: at), repo: repo)
    }

    private static func whole(_ s: String) -> Bool {
        pathRef.firstMatch(in: s, range: NSRange(location: 0, length: (s as NSString).length))?.range.length == (s as NSString).length
    }

    private static func linkPaths(_ text: String, repo: String) -> String {
        let ns = text as NSString
        var out = "", at = 0
        for m in pathRef.matches(in: text, range: NSRange(location: 0, length: ns.length)) {
            let ref = ns.substring(with: m.range)
            guard let f = file(ref, repo: repo) else { continue }
            out += ns.substring(with: NSRange(location: at, length: m.range.location - at))
            out += "[\(ref.replacingOccurrences(of: "_", with: "\\_"))](\(f.link.absoluteString))"
            at = m.range.location + m.range.length
        }
        return out + ns.substring(from: at)
    }
}

/// diffText colors added, removed and hunk lines.
func diffText(_ lines: [String]) -> AttributedString {
    var out = AttributedString()
    for (i, line) in lines.enumerated() {
        var part = AttributedString(line + (i < lines.count - 1 ? "\n" : ""))
        if line.hasPrefix("+++") || line.hasPrefix("---") || line.hasPrefix("diff --git") || line.hasPrefix("index ") {
            part.inlinePresentationIntent = .stronglyEmphasized
        } else if line.hasPrefix("+") {
            part.foregroundColor = .green
        } else if line.hasPrefix("-") {
            part.foregroundColor = .red
        } else if line.hasPrefix("@@") {
            part.foregroundColor = .blue
        }
        out += part
    }
    return out
}

/// Fence is the opening line of a fenced code block: three or more
/// backticks or tildes. As in CommonMark, only a run of the same
/// character at least as long, with nothing after it, closes the block.
struct Fence {
    var marker: Character
    var count: Int
    var info: String

    init?(_ line: String) {
        let trimmed = line.drop { $0 == " " }
        guard let first = trimmed.first, first == "`" || first == "~" else { return nil }
        let run = trimmed.prefix { $0 == first }.count
        guard run >= 3 else { return nil }
        let info = trimmed.dropFirst(run).trimmingCharacters(in: .whitespaces)
        if first == "`" && info.contains("`") { return nil }
        marker = first
        count = run
        self.info = info
    }

    func closes(_ line: String) -> Bool {
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        return trimmed.count >= count && trimmed.allSatisfy { $0 == marker }
    }
}
