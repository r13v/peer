import Foundation

// The types mirror the JSON of `peer app`; Go stays the only code that
// reads or writes the store.

struct Member: Decodable, Hashable {
    var role: String
    var agent: String?
    var exited: Bool?
}

struct Session: Decodable, Hashable {
    var id: String
    var repo: String
    var members: [Member]
    var startedAt: String
    var endedAt: String?
    var endedReason: String?

    var active: Bool { endedAt == nil }
    var project: String { URL(fileURLWithPath: repo).lastPathComponent }
}

struct Message: Decodable, Hashable, Identifiable {
    var id: String
    var at: String
    var from: String
    var to: String
    var text: String
}

/// MemberState is what a participant is doing: waiting, busy, exited or
/// ended. Peer infers it from wait polling, so busy only means that the
/// member has not polled in the last two seconds.
struct MemberState: Decodable, Hashable {
    var role: String
    var agent: String?
    var state: String
    var since: String
}

struct RoomSummary: Decodable, Hashable, Identifiable {
    var store: String
    var session: Session
    var messages: Int
    var status: String
    var members: [MemberState]

    /// Room IDs repeat across checkouts, so the store is part of the key.
    var id: String { store + "/" + session.id }
}

struct LogEntry: Decodable, Hashable {
    var offset: Int64
    var at: String?
    var kind: String
    var text: String
    var failed: Bool?
}

/// LogRow is a log entry with an ID that stays put when rows before it
/// are dropped or loaded: one log line can hold several entries.
struct LogRow: Identifiable, Hashable {
    var id: String
    var entry: LogEntry

    static func rows(_ entries: [LogEntry]) -> [LogRow] {
        var seen: [Int64: Int] = [:]
        return entries.map { e in
            let n = seen[e.offset, default: 0]
            seen[e.offset] = n + 1
            return LogRow(id: "\(e.offset).\(n)", entry: e)
        }
    }
}

struct Snapshot: Decodable {
    var session: Session
    var status: String
    var members: [MemberState]
    var messages: [Message]
    var next: Int64
    var logRoles: [String]
    var logRole: String?
    var log: [LogEntry]
    var logNext: Int64
    var logStart: Int64
}

struct LogPart: Decodable {
    var log: [LogEntry]
    var logStart: Int64
}

struct PeerError: LocalizedError {
    var message: String
    var errorDescription: String? { message }
}

/// PeerCLI runs the peer binary bundled in the app, passing arguments and
/// stdin as data, never through a shell.
enum PeerCLI {
    static var executable: URL {
        if let bundled = Bundle.main.url(forResource: "peer", withExtension: nil) {
            return bundled
        }
        // Outside Peer.app, as with swift run, use the CLI on PATH.
        let path = ProcessInfo.processInfo.environment["PATH"] ?? "/usr/bin:/bin"
        for dir in path.split(separator: ":").map(String.init) + ["/opt/homebrew/bin", "/usr/local/bin"] {
            let url = URL(fileURLWithPath: dir).appendingPathComponent("peer")
            if FileManager.default.isExecutableFile(atPath: url.path) { return url }
        }
        return URL(fileURLWithPath: "/usr/local/bin/peer")
    }

    static func run(_ args: [String], input: String? = nil) async throws -> Data {
        let url = executable
        return try await Task.detached {
            let process = Process()
            process.executableURL = url
            process.arguments = ["app"] + args
            let out = Pipe(), err = Pipe()
            process.standardOutput = out
            process.standardError = err
            let stdin = Pipe()
            process.standardInput = stdin
            try process.run()
            // peer may exit before reading, as on a bad argument; the
            // write then fails instead of raising SIGPIPE.
            if let input { try? stdin.fileHandleForWriting.write(contentsOf: Data(input.utf8)) }
            try? stdin.fileHandleForWriting.close()
            // Read before waiting, so a full pipe cannot block peer.
            let data = out.fileHandleForReading.readDataToEndOfFile()
            let errData = err.fileHandleForReading.readDataToEndOfFile()
            process.waitUntilExit()
            guard process.terminationStatus == 0 else {
                let text = String(decoding: errData, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
                throw PeerError(message: text.replacingOccurrences(of: "peer: ", with: "", options: .anchored))
            }
            return data
        }.value
    }

    static func decode<T: Decodable>(_ type: T.Type, _ args: [String]) async throws -> T {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return try decoder.decode(type, from: try await run(args))
    }
}
