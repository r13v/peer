import AppKit
import QuickLook
import SwiftUI
import Textual

@main
struct PeerApp: App {
    @State private var rooms = Rooms()

    var body: some Scene {
        Window("Peer", id: "main") {
            RoomsView().environment(rooms)
        }
        .defaultSize(width: 1240, height: 780)

        MenuBarExtra {
            MenuView().environment(rooms)
        } label: {
            MenuLabel().environment(rooms)
        }

        Settings {
            SettingsView()
        }
    }
}

/// MenuLabel is always on screen, so it opens the main window when a
/// notification asks for it.
struct MenuLabel: View {
    @Environment(Rooms.self) private var rooms
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Label("Peer", systemImage: rooms.activeCount > 0 ? "bubble.left.and.bubble.right.fill" : "bubble.left.and.bubble.right")
            .onChange(of: rooms.openRequest) {
                openWindow(id: "main")
                NSApp.activate()
            }
    }
}

struct MenuView: View {
    @Environment(Rooms.self) private var rooms

    var body: some View {
        let active = rooms.rooms.filter(\.session.active)
        Text(active.isEmpty ? "No active rooms" : "\(active.count) active")
        ForEach(active) { room in
            Button("\(room.session.project) · \(room.session.id) — \(room.status)") { rooms.open(room.id) }
        }
        Divider()
        Button("Open Peer") { rooms.openRequest += 1 }.keyboardShortcut("o")
        SettingsLink { Text("Settings…") }.keyboardShortcut(",")
        Button("Quit") { NSApp.terminate(nil) }.keyboardShortcut("q")
    }
}

struct SettingsView: View {
    @AppStorage("editorURL") private var editorURL = ""

    var body: some View {
        Form {
            TextField("Editor URL", text: $editorURL, prompt: Text("vscode://file/{path}:{line}"))
            Text("Opens file links from messages and logs. {path}, {line} and {column} are replaced; leave empty to open files in their default app.")
                .font(.caption).foregroundStyle(.secondary)
        }
        .formStyle(.grouped)
        .frame(width: 460)
    }
}

enum RoomFilter: String, CaseIterable {
    case all = "All Rooms", active = "Active", ended = "Ended"
}

// MARK: - Sidebar

struct RoomsView: View {
    @Environment(Rooms.self) private var rooms
    @State private var search = ""
    @AppStorage("filter") private var filter = RoomFilter.all
    @FocusState private var searching: Bool

    /// projects groups the shown rooms by checkout, in the order peer
    /// lists them: checkouts with active rooms first.
    private var projects: [(repo: String, rooms: [RoomSummary])] {
        let shown = rooms.rooms.filter { room in
            (filter == .all || room.session.active == (filter == .active))
                && (search.isEmpty || room.session.id.localizedCaseInsensitiveContains(search) || room.session.project.localizedCaseInsensitiveContains(search))
        }
        var order: [String] = []
        var groups: [String: [RoomSummary]] = [:]
        for room in shown {
            if groups[room.session.repo] == nil { order.append(room.session.repo) }
            groups[room.session.repo, default: []].append(room)
        }
        return order.map { ($0, groups[$0]!) }
    }

    var body: some View {
        @Bindable var rooms = rooms
        NavigationSplitView {
            List(selection: $rooms.selection) {
                ForEach(projects, id: \.repo) { project in
                    Section {
                        ForEach(project.rooms) { RoomRow(room: $0).tag($0.id) }
                    } header: {
                        Label(URL(fileURLWithPath: project.repo).lastPathComponent, systemImage: "folder")
                            .help(project.repo)
                    }
                }
            }
            .listStyle(.sidebar)
            .searchable(text: $search, placement: .sidebar, prompt: "Search Rooms")
            .searchFocused($searching)
            .overlay {
                if projects.isEmpty {
                    ContentUnavailableView(search.isEmpty ? "No Rooms" : "No Matches", systemImage: search.isEmpty ? "bubble.left.and.bubble.right" : "magnifyingglass")
                }
            }
            .safeAreaInset(edge: .bottom, spacing: 0) { footer }
            .background {
                // ⌘K jumps to the room search.
                Button("Find Room") { searching = true }
                    .keyboardShortcut("k")
                    .opacity(0)
            }
            .navigationSplitViewColumnWidth(min: 230, ideal: 270)
        } detail: {
            if let room = rooms.selected {
                RoomView(room: room).id(room.id)
            } else {
                ContentUnavailableView("Select a Room", systemImage: "bubble.left.and.bubble.right", description: Text("Press ⌘K to find one."))
            }
        }
    }

    /// footer filters the list, as Mail's sidebar does, and counts rooms.
    private var footer: some View {
        HStack(spacing: 8) {
            Menu {
                Picker("Show", selection: $filter) {
                    ForEach(RoomFilter.allCases, id: \.self) { Text($0.rawValue) }
                }
                .pickerStyle(.inline)
            } label: {
                Image(systemName: filter == .all ? "line.3.horizontal.decrease.circle" : "line.3.horizontal.decrease.circle.fill")
                    .font(.title3)
                    .foregroundStyle(filter == .all ? Color.secondary : Color.accentColor)
            }
            .menuStyle(.button)
            .buttonStyle(.borderless)
            .menuIndicator(.hidden)
            .fixedSize()
            .help("Filter rooms")
            Text(filter == .all ? "\(rooms.activeCount) active of \(rooms.rooms.count)" : filter.rawValue)
                .font(.callout)
                .foregroundStyle(.secondary)
            Spacer()
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 9)
    }
}

struct RoomRow: View {
    @Environment(Rooms.self) private var rooms
    var room: RoomSummary

    var body: some View {
        let muted = rooms.muted.contains(room.id)
        let busy = room.members.contains { $0.state == "busy" }
        HStack(spacing: 10) {
            StatusDot(color: room.session.active ? (busy ? .orange : .green) : .secondary.opacity(0.5), glow: room.session.active)
            VStack(alignment: .leading, spacing: 2) {
                Text(room.session.id)
                    .font(.body.weight(room.session.active ? .semibold : .regular))
                    .lineLimit(1)
                Text("\(room.session.members.count) members · \(room.messages) messages")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 4)
            VStack(alignment: .trailing, spacing: 2) {
                TimelineView(.periodic(from: .now, by: 30)) { context in
                    Text(short(room.session.endedAt ?? room.session.startedAt, now: context.date))
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                        .monospacedDigit()
                }
                if muted {
                    Image(systemName: "bell.slash.fill").font(.caption2).foregroundStyle(.tertiary)
                }
            }
        }
        .padding(.vertical, 3)
        .contextMenu {
            Button(muted ? "Unmute Notifications" : "Mute Notifications", systemImage: muted ? "bell" : "bell.slash") {
                if muted { rooms.muted.remove(room.id) } else { rooms.muted.insert(room.id) }
            }
            Button("Copy Room ID", systemImage: "doc.on.doc") { copy(room.session.id) }
            Button("Reveal Checkout in Finder", systemImage: "folder") { reveal(room.session.repo) }
        }
    }
}

// MARK: - Room

struct RoomView: View {
    @Environment(Rooms.self) private var rooms
    var room: RoomSummary
    @State private var confirmClose = false
    @State private var adding = false
    @State private var actionError: String?
    @State private var sending = false
    @State private var sent = false
    @State private var preview: URL?
    @AppStorage("inspector") private var inspector = true

    private var session: Session { rooms.snapshot?.session ?? room.session }
    private var to: String { rooms.recipients[room.id] ?? "*" }
    private var draftKey: String { room.id + "|" + to }

    var body: some View {
        VSplitView {
            Transcript(messages: rooms.messages, repo: session.repo, preview: $preview)
                .frame(minHeight: 220)
                .safeAreaInset(edge: .bottom, spacing: 0) {
                    if session.active { compose }
                }
            // Each member's log scrolls and expands on its own.
            MemberLog(repo: session.repo, preview: $preview)
                .id(rooms.logRole)
                .frame(minHeight: 140, idealHeight: 260)
        }
        .environment(\.openURL, OpenURLAction { url in
            guard let file = FileRef(link: url) else { return .systemAction }
            file.open()
            return .handled
        })
        .quickLookPreview($preview)
        .navigationTitle(session.id)
        .navigationSubtitle(session.project)
        .inspector(isPresented: $inspector) {
            Inspector(room: room).inspectorColumnWidth(min: 250, ideal: 280, max: 360)
        }
        .toolbar {
            if session.active {
                ToolbarItemGroup {
                    Button("Add Member", systemImage: "person.badge.plus") { adding = true }
                        .help("Ask the writer to add a member")
                    Button("Close Room", systemImage: "xmark.circle") { confirmClose = true }
                        .help("End this room")
                }
            }
            ToolbarItem {
                Button("Inspector", systemImage: "sidebar.trailing") { inspector.toggle() }
                    .help(inspector ? "Hide Inspector" : "Show Inspector")
            }
        }
        .confirmationDialog("Close room \(session.id)?", isPresented: $confirmClose) {
            Button("Close Room", role: .destructive) { act { try await rooms.close(room) } }
        } message: {
            Text("Members see that the room has ended. Their processes are not killed.")
        }
        .sheet(isPresented: $adding) {
            AddMemberSheet { agent, description in try await rooms.add(agent: agent, description: description, in: room) }
        }
    }

    /// compose is a glass capsule floating over the transcript, with the
    /// recipient as a menu inside it, as in Messages.
    private var compose: some View {
        let text = Binding(get: { rooms.drafts[draftKey] ?? "" }, set: { rooms.drafts[draftKey] = $0.isEmpty ? nil : $0 })
        let empty = text.wrappedValue.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        return VStack(spacing: 6) {
            if let actionError {
                Label(actionError, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption).foregroundStyle(.red)
                    .transition(.opacity)
            } else if sent {
                Label("Sent — members read it on their next peer wait", systemImage: "checkmark.circle.fill")
                    .font(.caption).foregroundStyle(.secondary)
                    .transition(.opacity)
            }
            HStack(alignment: .bottom, spacing: 8) {
                Menu {
                    Picker("To", selection: Binding(get: { to }, set: { rooms.recipients[room.id] = $0 })) {
                        Label("Everyone", systemImage: "person.3").tag("*")
                        Divider()
                        ForEach(session.members, id: \.role) { m in
                            Text(m.agent.map { "\(m.role) · \($0)" } ?? m.role).tag(m.role)
                        }
                    }
                    .pickerStyle(.inline)
                } label: {
                    HStack(spacing: 3) {
                        Text(to == "*" ? "Everyone" : to)
                        Image(systemName: "chevron.up.chevron.down").font(.caption2)
                    }
                    .font(.callout.weight(.medium))
                    .foregroundStyle(to == "*" ? Color.secondary : roleColor(to))
                    .padding(.horizontal, 10)
                    .padding(.vertical, 6)
                    .background(.quaternary.opacity(0.6), in: Capsule())
                }
                .menuStyle(.button)
                .buttonStyle(.plain)
                .menuIndicator(.hidden)
                .fixedSize()
                .help("Recipient")
                TextField("Message", text: text, axis: .vertical)
                    .textFieldStyle(.plain)
                    .lineLimit(1...8)
                    .padding(.vertical, 6)
                    .onSubmit(send)
                Group {
                    if sending {
                        ProgressView().controlSize(.small)
                    } else {
                        Button(action: send) {
                            Image(systemName: "arrow.up.circle.fill")
                                .font(.system(size: 26))
                                .symbolRenderingMode(.palette)
                                .foregroundStyle(.white, empty ? Color.secondary.opacity(0.4) : Color.accentColor)
                        }
                        .buttonStyle(.plain)
                        .keyboardShortcut(.return, modifiers: .command)
                        .disabled(empty)
                        .help("Send (⌘↩)")
                    }
                }
                .frame(width: 28, height: 28)
            }
            .padding(.leading, 6)
            .padding(.trailing, 6)
            .padding(.vertical, 4)
            .glassEffect(.regular, in: .rect(cornerRadius: 20))
        }
        .frame(maxWidth: Transcript.width)
        .padding(.horizontal, 20)
        .padding(.bottom, 14)
        .animation(.default, value: sent)
        .animation(.default, value: actionError)
    }

    private func send() {
        let key = draftKey, target = to
        let body = rooms.drafts[key] ?? ""
        guard !sending, !body.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        sending = true
        sent = false
        act {
            defer { sending = false }
            try await rooms.post(body, to: target, in: room)
            // Keep what was typed while the message was being sent.
            if rooms.drafts[key] == body { rooms.drafts[key] = nil }
            sent = true
            Task {
                try? await Task.sleep(for: .seconds(4))
                sent = false
            }
        }
    }

    private func act(_ action: @escaping () async throws -> Void) {
        Task {
            do {
                actionError = nil
                try await action()
            } catch {
                actionError = error.localizedDescription
            }
        }
    }
}

// MARK: - Inspector

/// Inspector shows who is in the room and what each is doing, how fresh
/// that is, and picks the member whose log is shown.
struct Inspector: View {
    @Environment(Rooms.self) private var rooms
    var room: RoomSummary

    var body: some View {
        let session = rooms.snapshot?.session ?? room.session
        let members = rooms.snapshot?.members ?? room.members
        let logRoles = rooms.snapshot?.logRoles ?? []
        TimelineView(.periodic(from: .now, by: 1)) { context in
            ScrollView {
                VStack(alignment: .leading, spacing: 22) {
                    header(session)
                    VStack(alignment: .leading, spacing: 8) {
                        SectionTitle("Members", count: members.count)
                        VStack(spacing: 2) {
                            ForEach(members, id: \.role) { m in
                                MemberCard(member: m, now: context.date, hasLog: logRoles.contains(m.role), selected: rooms.logRole == m.role) {
                                    rooms.logRole = m.role
                                }
                            }
                        }
                    }
                    VStack(alignment: .leading, spacing: 8) {
                        SectionTitle("Details")
                        Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 12, verticalSpacing: 7) {
                            detail("Started", time(session.startedAt))
                            detail(session.active ? "Running" : "Lasted", duration(session.startedAt, until: session.endedAt.flatMap(date) ?? context.date))
                            if session.endedAt != nil {
                                detail("Ended", session.endedReason ?? "by the writer")
                            }
                            detail("Messages", "\(rooms.messages.count)")
                        }
                        .font(.callout)
                    }
                }
                .padding(18)
            }
            .safeAreaInset(edge: .bottom, spacing: 0) { freshness(now: context.date) }
        }
    }

    private func header(_ session: Session) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline) {
                Text(session.id).font(.title3.weight(.semibold)).lineLimit(2)
                Spacer()
                Pill(text: session.active ? "Active" : "Ended", color: session.active ? .green : .secondary)
            }
            Button {
                reveal(session.repo)
            } label: {
                Label(abbreviate(session.repo), systemImage: "folder")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            .buttonStyle(.plain)
            .help("Reveal \(session.repo) in Finder")
        }
    }

    @ViewBuilder
    private func detail(_ name: String, _ value: String) -> some View {
        GridRow {
            Text(name).foregroundStyle(.secondary)
            Text(value).textSelection(.enabled)
        }
    }

    private func freshness(now: Date) -> some View {
        HStack(spacing: 6) {
            if let error = rooms.error {
                Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.red)
                Text(error).lineLimit(2)
            } else if let updated = rooms.updated {
                StatusDot(color: .green, glow: false, size: 6)
                Text("Updated \(max(0, Int(now.timeIntervalSince(updated)))) s ago")
            }
            Spacer()
        }
        .font(.caption)
        .foregroundStyle(.secondary)
        .padding(.horizontal, 18)
        .padding(.vertical, 10)
    }
}

struct MemberCard: View {
    var member: MemberState
    var now: Date
    var hasLog: Bool
    var selected: Bool
    var select: () -> Void
    @State private var hover = false

    var body: some View {
        // A member without a log is no button; disabling one would dim its state.
        Group {
            if hasLog {
                Button(action: select) { card }.buttonStyle(.plain)
            } else {
                card
            }
        }
        .onHover { hover = $0 }
            .help(hasLog ? "Show \(member.role)'s log" : "\(member.role) runs outside peer, so it has no log")
            .accessibilityLabel("\(member.role), \(member.agent ?? "joined by hand"), \(state)")
    }

    private var card: some View {
        HStack(spacing: 10) {
            Avatar(role: member.role, agent: member.agent)
            VStack(alignment: .leading, spacing: 1) {
                Text(member.role).font(.callout.weight(.semibold))
                Text(member.agent ?? "joined by hand").font(.caption).foregroundStyle(.secondary)
            }
            Spacer(minLength: 4)
            Pill(text: state, color: stateColor(member.state))
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 7)
        .background {
            RoundedRectangle(cornerRadius: 9)
                .fill(selected ? Color.accentColor.opacity(0.14) : hover && hasLog ? Color.primary.opacity(0.05) : .clear)
        }
        .contentShape(RoundedRectangle(cornerRadius: 9))
    }

    private var state: String {
        switch member.state {
        case "busy": "Busy \(duration(member.since, until: now))"
        case "waiting": "Waiting"
        case "exited": "Exited"
        default: "Done"
        }
    }
}

// MARK: - Transcript

/// FollowScroll keeps a list at its end while the reader is there; once
/// they scroll up to read, new rows only count up in a jump button.
struct FollowScroll<Content: View>: View {
    private struct Edge: Equatable {
        var offset: CGFloat
        var end: Bool
    }

    var ids: [String]
    @ViewBuilder var content: () -> Content
    @State private var atEnd = true
    @State private var unseen = 0

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                content()
            }
            .defaultScrollAnchor(.bottom)
            .onScrollGeometryChange(for: Edge.self) { g in
                Edge(offset: g.contentOffset.y, end: g.contentOffset.y + g.containerSize.height >= g.contentSize.height - 40)
            } action: { old, new in
                // Content growing under a still list is not the reader
                // leaving the end; only a scroll changes where they are.
                guard old.offset != new.offset || new.end else { return }
                atEnd = new.end
                if new.end { unseen = 0 }
            }
            .onChange(of: ids.first) { old, _ in
                // Rows loaded above keep the reader's place.
                if let old, !atEnd, ids.contains(old) { proxy.scrollTo(old, anchor: .top) }
            }
            .onChange(of: ids.last) { old, last in
                guard let last, let old else { return }
                if atEnd {
                    proxy.scrollTo(last, anchor: .bottom)
                } else if let i = ids.lastIndex(of: old) {
                    unseen += ids.count - 1 - i
                }
            }
            .overlay(alignment: .bottom) {
                if unseen > 0, let last = ids.last {
                    Button {
                        withAnimation { proxy.scrollTo(last, anchor: .bottom) }
                        unseen = 0
                    } label: {
                        Label("\(unseen) new", systemImage: "arrow.down")
                            .font(.callout.weight(.medium))
                    }
                    .buttonStyle(.glass)
                    .padding(.bottom, 12)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
                }
            }
            .animation(.snappy, value: unseen > 0)
        }
    }
}

struct Transcript: View {
    /// width keeps lines at a readable length on wide windows.
    static let width: CGFloat = 780
    var messages: [Message]
    var repo: String
    @Binding var preview: URL?

    var body: some View {
        FollowScroll(ids: messages.map(\.id)) {
            LazyVStack(alignment: .leading, spacing: 0) {
                ForEach(Array(messages.enumerated()), id: \.element.id) { i, m in
                    let prev = i > 0 ? messages[i - 1] : nil
                    let day = dayLabel(m.at)
                    if day != prev.map({ dayLabel($0.at) }) {
                        DayDivider(label: day)
                    }
                    let grouped = prev.map { $0.from == m.from && $0.to == m.to && dayLabel($0.at) == day } ?? false
                    MessageRow(message: m, grouped: grouped, repo: repo, preview: $preview).id(m.id)
                }
            }
            .frame(maxWidth: Self.width)
            .padding(.horizontal, 24)
            .padding(.vertical, 16)
            .frame(maxWidth: .infinity)
        }
        .background(Color(nsColor: .textBackgroundColor))
        .overlay {
            if messages.isEmpty { ContentUnavailableView("No Messages Yet", systemImage: "text.bubble") }
        }
    }
}

struct DayDivider: View {
    var label: String

    var body: some View {
        HStack(spacing: 10) {
            line
            Text(label).font(.caption.weight(.medium)).foregroundStyle(.secondary)
            line
        }
        .padding(.vertical, 14)
    }

    private var line: some View { Rectangle().fill(.separator).frame(height: 1) }
}

struct MessageRow: View {
    var message: Message
    var grouped: Bool
    var repo: String
    @Binding var preview: URL?
    @State private var hover = false

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Group {
                if grouped {
                    Color.clear
                } else {
                    Avatar(role: message.from, agent: nil, size: 30)
                }
            }
            .frame(width: 30)
            VStack(alignment: .leading, spacing: 4) {
                if !grouped {
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Text(message.from).font(.callout.weight(.semibold)).foregroundStyle(roleColor(message.from))
                        Image(systemName: "arrow.right").font(.caption2).foregroundStyle(.tertiary)
                        Text(message.to == "*" ? "everyone" : message.to).font(.callout).foregroundStyle(.secondary)
                        Text(clock(message.at)).font(.caption).foregroundStyle(.tertiary).monospacedDigit()
                    }
                }
                MarkdownView(text: message.text, repo: repo)
            }
            .padding(message.from == "user" ? 10 : 0)
            .background {
                if message.from == "user" {
                    RoundedRectangle(cornerRadius: 10).fill(Color.orange.opacity(0.08))
                }
            }
        }
        .padding(.top, grouped ? 6 : 16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .contentShape(Rectangle())
        .onHover { hover = $0 }
        .overlay(alignment: .topTrailing) {
            if hover && grouped {
                Text(clock(message.at)).font(.caption2).foregroundStyle(.tertiary).monospacedDigit().padding(.top, 6)
            }
        }
        .contextMenu {
            Button("Copy Message", systemImage: "doc.on.doc") { copy(message.text) }
            FileMenu(files: Markup.files(message.text, repo: repo), preview: $preview)
        }
    }
}

/// MarkdownView renders a message or a member's words: Markdown through
/// Textual, with diff blocks colored line by line.
struct MarkdownView: View {
    var text: String
    var repo: String

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            ForEach(Array(Markup.segments(text, repo: repo).enumerated()), id: \.offset) { _, segment in
                switch segment {
                case .markdown(let text):
                    StructuredText(markdown: text).textual.textSelection(.enabled)
                case .diff(let lines):
                    DiffBlock(lines: lines)
                }
            }
        }
    }
}

struct DiffBlock: View {
    var lines: [String]

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            Text(diffText(lines))
                .font(.system(.callout, design: .monospaced))
                .textSelection(.enabled)
                .padding(12)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.primary.opacity(0.04), in: RoundedRectangle(cornerRadius: 8))
        .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(.separator.opacity(0.6)))
    }
}

/// FileMenu offers what can be done with the files a text names.
struct FileMenu: View {
    var files: [FileRef]
    @Binding var preview: URL?

    var body: some View {
        if !files.isEmpty {
            Divider()
            ForEach(files.prefix(10)) { f in
                Menu(f.label) {
                    Button("Open", systemImage: "arrow.up.forward.app") { f.open() }
                    Button("Reveal in Finder", systemImage: "folder") { f.reveal() }
                    Button("Quick Look", systemImage: "eye") { preview = f.url }
                    Button("Copy Path", systemImage: "doc.on.doc") { copy(f.path) }
                }
            }
        }
    }
}

// MARK: - Member log

struct MemberLog: View {
    @Environment(Rooms.self) private var rooms
    var repo: String
    @Binding var preview: URL?
    @State private var expanded: Set<String> = []

    var body: some View {
        @Bindable var rooms = rooms
        let roles = rooms.snapshot?.logRoles ?? []
        VStack(spacing: 0) {
            HStack(spacing: 10) {
                Label("Activity", systemImage: "terminal").font(.headline)
                if roles.count > 1 {
                    Picker("Member", selection: $rooms.logRole) {
                        ForEach(roles, id: \.self) { Text($0).tag(Optional($0)) }
                    }
                    .pickerStyle(.segmented)
                    .labelsHidden()
                    .fixedSize()
                } else if let role = rooms.logRole {
                    Text(role).foregroundStyle(roleColor(role)).font(.callout.weight(.medium))
                }
                Spacer()
                if !rooms.log.isEmpty {
                    Text("\(rooms.log.count) entries").font(.caption).foregroundStyle(.secondary).monospacedDigit()
                }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 9)
            Divider()
            FollowScroll(ids: rooms.log.map(\.id)) {
                LazyVStack(alignment: .leading, spacing: 6) {
                    if rooms.logStart > 0 {
                        HStack(spacing: 8) {
                            Text("Showing the last \(rooms.log.count) entries")
                            Button(rooms.loadingEarlier ? "Loading…" : "Load Earlier") { Task { await rooms.loadEarlier() } }
                                .buttonStyle(.link)
                                .disabled(rooms.loadingEarlier)
                        }
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 8)
                    }
                    ForEach(rooms.log) { row in
                        LogRowView(row: row, expanded: expanded.contains(row.id), repo: repo, preview: $preview) {
                            if expanded.contains(row.id) { expanded.remove(row.id) } else { expanded.insert(row.id) }
                        }
                        .id(row.id)
                    }
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 10)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .overlay {
                if rooms.log.isEmpty {
                    ContentUnavailableView(rooms.logRole == nil ? "No Background Members" : "No Activity Yet", systemImage: "terminal",
                                           description: Text(rooms.logRole == nil ? "Members that peer launches show what they do here." : "Waiting for the member's first output."))
                }
            }
        }
        .background(Color(nsColor: .windowBackgroundColor))
    }
}

struct LogRowView: View {
    var row: LogRow
    var expanded: Bool
    var repo: String
    @Binding var preview: URL?
    var toggle: () -> Void

    private static let collapsedLines = 8

    var body: some View {
        let e = row.entry
        let lines = e.text.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text(e.at.map(clock) ?? "")
                .font(.caption2)
                .foregroundStyle(.tertiary)
                .monospacedDigit()
                .frame(width: 54, alignment: .trailing)
            switch e.kind {
            case "text":
                MarkdownView(text: e.text, repo: repo).font(.body)
            case "tool":
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Image(systemName: e.failed == true ? "xmark.octagon.fill" : "chevron.right")
                        .font(.caption.weight(.bold))
                        .foregroundStyle(e.failed == true ? .red : .accentColor)
                    Text(e.text)
                        .font(.system(.callout, design: .monospaced).weight(.medium))
                        .foregroundStyle(e.failed == true ? .red : .primary)
                        .lineLimit(expanded ? nil : 3)
                        .textSelection(.enabled)
                }
            default:
                let collapsible = lines.count > Self.collapsedLines
                let shown = collapsible && !expanded ? Array(lines.prefix(Self.collapsedLines)) : lines
                VStack(alignment: .leading, spacing: 4) {
                    Text(Markup.isDiff(lines) ? diffText(shown) : AttributedString(shown.joined(separator: "\n")))
                        .font(.system(.caption, design: .monospaced))
                        .foregroundStyle(e.failed == true ? Color.red : .secondary)
                        .textSelection(.enabled)
                    if collapsible {
                        Button(expanded ? "Show Less" : "Show All \(lines.count) Lines", action: toggle)
                            .buttonStyle(.link).font(.caption)
                    }
                }
                .padding(.horizontal, 10)
                .padding(.vertical, 7)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color.primary.opacity(0.035), in: RoundedRectangle(cornerRadius: 7))
            }
        }
        .contextMenu {
            Button("Copy", systemImage: "doc.on.doc") { copy(e.text) }
            FileMenu(files: Markup.files(e.text, repo: repo), preview: $preview)
        }
    }
}

// MARK: - Add member

struct AddMemberSheet: View {
    var onAdd: (String, String) async throws -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var agent = "codex"
    @State private var description = ""
    @State private var asking = false
    @State private var error: String?

    var body: some View {
        Form {
            Section {
                Picker("Agent", selection: $agent) {
                    Text("Codex").tag("codex")
                    Text("Claude").tag("claude")
                }
                .pickerStyle(.segmented)
                TextField("Role", text: $description, prompt: Text("e.g. security reviewer"), axis: .vertical)
                    .lineLimit(2...5)
            } header: {
                Text("Add Member")
            } footer: {
                Text(error ?? "The writer chooses a role name, writes a brief and invites the member.")
                    .foregroundStyle(error == nil ? Color.secondary : .red)
            }
        }
        .formStyle(.grouped)
        .frame(width: 440)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            ToolbarItem(placement: .confirmationAction) {
                Button("Ask Writer") {
                    asking = true
                    Task {
                        defer { asking = false }
                        do {
                            try await onAdd(agent, description)
                            dismiss()
                        } catch {
                            self.error = error.localizedDescription
                        }
                    }
                }
                .disabled(asking || description.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
    }
}
