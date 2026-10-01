import Foundation
import Testing
@testable import Peer

@MainActor @Suite struct MarkupTests {
    let repo: String

    init() throws {
        repo = FileManager.default.temporaryDirectory.appendingPathComponent("peer-markup-\(UUID().uuidString)").path
        try FileManager.default.createDirectory(atPath: repo + "/src", withIntermediateDirectories: true)
        for f in ["src/main.go", "my_file.go"] { FileManager.default.createFile(atPath: repo + "/" + f, contents: nil) }
    }

    @Test func linksExistingFiles() throws {
        let out = Markup.render("see src/main.go:12:3 and `src/main.go` and missing.go", repo: repo)
        #expect(out.contains("[src/main.go:12:3](peer-file:") && out.contains("/src/main.go?line=12&column=3)"))
        #expect(out.contains("[`src/main.go`](peer-file:"))
        #expect(out.contains(" missing.go"))
        let ref = try #require(Markup.files("src/main.go:12:3", repo: repo).first)
        let back = try #require(FileRef(link: ref.link))
        #expect(back.line == 12 && back.column == 3 && back.path == repo + "/src/main.go")
    }

    @Test func leavesLinksURLsAndFencesAlone() {
        let text = "[x](src/main.go) https://e.com/src/main.go\n```\nsrc/main.go\n```"
        #expect(Markup.render(text, repo: repo) == text)
    }

    @Test func longerFenceHoldsShorterOne() {
        let text = "````text\n```\nREADME.md\n```\n````"
        FileManager.default.createFile(atPath: repo + "/README.md", contents: nil)
        #expect(Markup.render(text, repo: repo) == text)
        #expect(Markup.render("README.md", repo: repo).contains("peer-file"))
    }

    @Test func escapesUnderscores() {
        #expect(Markup.render("my_file.go", repo: repo).hasPrefix("[my\\_file.go]("))
    }

    @Test func splitsDiffBlocks() {
        let text = "intro\n```\n@@ -1 +1 @@\n-a\n+b\n```\nafter"
        let segments = Markup.segments(text, repo: repo)
        #expect(segments.count == 3)
        #expect(segments[1] == .diff(["@@ -1 +1 @@", "-a", "+b"]))
    }

    @Test func plainListIsNotDiff() {
        #expect(!Markup.isDiff(["- a", "+ b"]))
        #expect(Markup.segments("```\n- a\n+ b\n```", repo: repo).count == 1)
    }
}
