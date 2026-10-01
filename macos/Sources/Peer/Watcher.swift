import CoreServices
import Foundation

/// StoreWatcher calls onChange when a file under peer's store changes,
/// through FSEvents. Cursor and lock files are skipped: every peer wait
/// rewrites its cursor five times a second.
// Its stream calls back on the main queue only, so it is safe to send.
final class StoreWatcher: @unchecked Sendable {
    private var stream: FSEventStreamRef?
    private let onChange: @MainActor () -> Void

    /// home is the store's base: PEER_HOME, or ~/.peer, as peer's reposDir.
    static var home: URL {
        if let home = ProcessInfo.processInfo.environment["PEER_HOME"], !home.isEmpty {
            return URL(fileURLWithPath: home)
        }
        return FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".peer")
    }

    init(onChange: @escaping @MainActor () -> Void) {
        self.onChange = onChange
        let repos = Self.home.appendingPathComponent("repos")
        try? FileManager.default.createDirectory(at: repos, withIntermediateDirectories: true)
        var context = FSEventStreamContext(version: 0, info: Unmanaged.passUnretained(self).toOpaque(), retain: nil, release: nil, copyDescription: nil)
        let callback: FSEventStreamCallback = { _, info, _, paths, _, _ in
            guard let info else { return }
            let watcher = Unmanaged<StoreWatcher>.fromOpaque(info).takeUnretainedValue()
            let changed = unsafeBitCast(paths, to: NSArray.self).compactMap { $0 as? String }
            guard changed.contains(where: StoreWatcher.relevant) else { return }
            MainActor.assumeIsolated { watcher.onChange() }
        }
        let flags = kFSEventStreamCreateFlagFileEvents | kFSEventStreamCreateFlagUseCFTypes | kFSEventStreamCreateFlagNoDefer
        stream = FSEventStreamCreate(nil, callback, &context, [repos.path] as CFArray, FSEventStreamEventId(kFSEventStreamEventIdSinceNow), 0.2, FSEventStreamCreateFlags(flags))
        if let stream {
            FSEventStreamSetDispatchQueue(stream, .main)
            FSEventStreamStart(stream)
        }
    }

    deinit {
        if let stream {
            FSEventStreamStop(stream)
            FSEventStreamInvalidate(stream)
            FSEventStreamRelease(stream)
        }
    }

    static func relevant(_ path: String) -> Bool {
        let name = (path as NSString).lastPathComponent
        return !(name.hasPrefix("cursor-") || name.hasPrefix(".peer-") || name == ".lock")
    }
}
