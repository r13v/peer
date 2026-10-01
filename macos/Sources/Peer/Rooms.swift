import AppKit
import Foundation
import Observation
import UserNotifications

/// Rooms reads peer when its store changes, and every few seconds so that
/// busy times and waiting states stay current: the list of every room,
/// and whatever the selected room gained since the last read. Reading
/// never moves a participant's cursor, and quitting the app leaves rooms
/// running.
@MainActor @Observable
final class Rooms {
    var rooms: [RoomSummary] = []
    var error: String?
    /// updated is when peer last answered.
    var updated: Date?

    var selection: String? {
        didSet {
            guard selection != oldValue else { return }
            UserDefaults.standard.set(selection, forKey: "selection")
            if let oldValue { seen[oldValue] = (messages, next) }
            resetRoom()
            // Show what was read of the room before at once, then read
            // what it gained without waiting for the next change.
            if let selection, let cached = seen[selection] { (messages, next) = cached }
            Task { await poll() }
        }
    }
    var snapshot: Snapshot?
    var messages: [Message] = []
    var log: [LogRow] = []
    /// logStart is where the shown log begins; above 0, earlier entries exist.
    private(set) var logStart: Int64 = 0
    private(set) var loadingEarlier = false
    var logRole: String? {
        didSet {
            guard logRole != oldValue else { return }
            resetLog()
            Task { await poll() }
        }
    }

    /// drafts keeps unsent text by room and recipient, and recipients the
    /// chosen recipient by room, so switching rooms loses nothing.
    var drafts: [String: String] = UserDefaults.standard.dictionary(forKey: "drafts") as? [String: String] ?? [:] {
        didSet { UserDefaults.standard.set(drafts, forKey: "drafts") }
    }
    var recipients: [String: String] = UserDefaults.standard.dictionary(forKey: "recipients") as? [String: String] ?? [:] {
        didSet { UserDefaults.standard.set(recipients, forKey: "recipients") }
    }
    var muted: Set<String> = Set(UserDefaults.standard.stringArray(forKey: "muted") ?? []) {
        didSet { UserDefaults.standard.set(Array(muted), forKey: "muted") }
    }
    /// openRequest asks the menu bar label, which can open windows, to
    /// show the main window, as after a notification is clicked.
    var openRequest = 0

    private var next: Int64 = 0
    /// seen keeps the transcripts of rooms viewed before, so switching
    /// back reads only what is new.
    private var seen: [String: (messages: [Message], next: Int64)] = [:]
    private var logNext: Int64 = 0
    private var reading = false
    private var again = false
    /// gen changes with each reset, so a read of the room as it was
    /// before is dropped, as the TUI's poll does.
    private var gen = 0
    private var known: Set<String>?
    private static let logLimit = 2000
    private var logLimit = Rooms.logLimit
    private var watcher: StoreWatcher?
    private let notifications = NotificationDelegate()

    var selected: RoomSummary? { rooms.first { $0.id == selection } }
    var activeCount: Int { rooms.filter(\.session.active).count }

    init() {
        signal(SIGPIPE, SIG_IGN)
        selection = UserDefaults.standard.string(forKey: "selection")
        if Bundle.main.bundleIdentifier != nil {
            notifications.rooms = self
            UNUserNotificationCenter.current().delegate = notifications
            UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound]) { _, _ in }
        }
        watcher = StoreWatcher { [weak self] in Task { await self?.poll() } }
        Task { [weak self] in
            while let self {
                await self.poll()
                try? await Task.sleep(for: .seconds(3))
            }
        }
    }

    private func resetRoom() {
        gen += 1
        snapshot = nil
        messages = []
        next = 0
        logRole = nil
        resetLog()
    }

    private func resetLog() {
        gen += 1
        log = []
        logNext = 0
        logStart = 0
        logLimit = Self.logLimit
    }

    /// poll reads peer; a change seen while a read runs reads again after it.
    func poll() async {
        guard !reading else {
            again = true
            return
        }
        reading = true
        defer { reading = false }
        repeat {
            again = false
            do {
                rooms = try await PeerCLI.decode([RoomSummary].self, ["rooms"])
                let ids = Set(rooms.map(\.id))
                seen = seen.filter { ids.contains($0.key) }
                notifyChanges()
                if let room = selected { try await read(room) }
                error = nil
                updated = .now
            } catch {
                self.error = error.localizedDescription
            }
        } while again
    }

    private func read(_ room: RoomSummary) async throws {
        let wanted = logRole, readGen = gen
        var args = ["room", room.session.id, "--store", room.store, "--after", String(next), "--log-after", String(logNext)]
        if let wanted { args += ["--log", wanted] }
        let snap = try await PeerCLI.decode(Snapshot.self, args)
        // The selection or log may have changed while peer ran.
        guard selection == room.id, gen == readGen else { return }
        messages += snap.messages
        next = snap.next
        snapshot = snap
        if snap.logRole != wanted {
            logRole = snap.logRole
        }
        if logNext == 0 { logStart = snap.logStart }
        log += LogRow.rows(snap.log)
        logNext = snap.logNext
        if log.count > logLimit, !loadingEarlier {
            // Drop whole lines, so loading earlier reads the rest of a
            // line back with its entries.
            var drop = log.count - logLimit
            while drop < log.count, drop > 0, log[drop].entry.offset == log[drop - 1].entry.offset { drop += 1 }
            log.removeFirst(drop)
            logStart = log.first?.entry.offset ?? logNext
        }
    }

    /// loadEarlier reads the part of the log before the shown entries.
    func loadEarlier() async {
        guard let room = selected, let role = logRole, logStart > 0, !loadingEarlier else { return }
        loadingEarlier = true
        defer { loadingEarlier = false }
        let readGen = gen, before = logStart
        do {
            let part = try await PeerCLI.decode(LogPart.self, ["log", room.session.id, "--store", room.store, "--log", role, "--before", String(before)])
            // Trimming waits while this runs, so the part ends where the
            // shown log begins.
            guard selection == room.id, gen == readGen, logRole == role, logStart == before else { return }
            // Rows of the same line keep their IDs: a part ends at a line.
            log = LogRow.rows(part.log) + log
            logStart = part.logStart
            // History read on request stays until the room or log changes.
            logLimit = .max
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// notifyChanges posts each room ending and member exiting once,
    /// except for muted rooms; the first list only records what has
    /// already happened.
    private func notifyChanges() {
        var events: [String: (room: String, text: String)] = [:]
        for room in rooms {
            let name = "\(room.session.project) · \(room.session.id)"
            if !room.session.active {
                var text = "\(name) ended"
                if let reason = room.session.endedReason { text += ": \(reason)" }
                events["end/\(room.id)"] = (room.id, text)
            }
            for m in room.session.members where m.exited == true {
                events["exit/\(room.id)/\(m.role)"] = (room.id, "\(m.role) left \(name)")
            }
        }
        defer { known = Set(events.keys) }
        guard let known, Bundle.main.bundleIdentifier != nil else { return }
        for (key, event) in events where !known.contains(key) && !muted.contains(event.room) {
            let content = UNMutableNotificationContent()
            content.title = "Peer"
            content.body = event.text
            content.userInfo = ["room": event.room]
            UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: key, content: content, trigger: nil))
        }
    }

    func open(_ room: String) {
        selection = room
        openRequest += 1
    }

    func post(_ text: String, to: String, in room: RoomSummary) async throws {
        _ = try await PeerCLI.run(["post", room.session.id, "--store", room.store, "--to", to], input: text)
        await poll()
    }

    func add(agent: String, description: String, in room: RoomSummary) async throws {
        _ = try await PeerCLI.run(["add", room.session.id, "--store", room.store, "--agent", agent], input: description)
        await poll()
    }

    func close(_ room: RoomSummary) async throws {
        _ = try await PeerCLI.run(["close", room.session.id, "--store", room.store])
        await poll()
    }
}

/// NotificationDelegate opens the room of a clicked notification, and
/// shows notifications while the app is in front too.
final class NotificationDelegate: NSObject, UNUserNotificationCenterDelegate, @unchecked Sendable {
    @MainActor weak var rooms: Rooms?

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        guard let room = response.notification.request.content.userInfo["room"] as? String else { return }
        await MainActor.run { rooms?.open(room) }
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification) async -> UNNotificationPresentationOptions {
        [.banner, .sound]
    }
}
