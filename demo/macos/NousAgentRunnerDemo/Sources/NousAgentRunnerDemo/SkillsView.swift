import SwiftUI
#if canImport(AppKit)
import AppKit
#endif

private struct SkillItem: Identifiable, Hashable {
    let id: String
    let name: String
    let url: URL
    let hasSkillMD: Bool
}

struct SkillsView: View {
    @Environment(\.dismiss) private var dismiss

    @State private var statusText = ""
    @State private var skills: [SkillItem] = []
    @State private var showCreate = false
    @State private var newSkillName = ""
    @State private var createErrorText = ""
    @State private var pendingDelete: SkillItem?

    private var skillsDirURL: URL { DemoPaths.skillsDirURL() }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Skills")
                .font(.title3)

            GroupBox("Skills directory") {
                VStack(alignment: .leading, spacing: 8) {
                    Text(skillsDirURL.path)
                        .font(.system(.caption, design: .monospaced))
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)

                    HStack {
                        Button("Open Folder") { openURL(skillsDirURL) }
                        Button("Refresh") { Task { await refresh() } }
                        Spacer()
                        Text("count: \(skills.count)")
                            .font(.system(.caption, design: .monospaced))
                            .foregroundStyle(.secondary)
                    }
                }
            }

            GroupBox("Installed skills") {
                ScrollView {
                    VStack(alignment: .leading, spacing: 8) {
                        ForEach(skills) { s in
                            HStack(spacing: 10) {
                                Text(s.name)
                                    .font(.system(.body, design: .monospaced))
                                    .lineLimit(1)
                                    .truncationMode(.middle)

                                Spacer()

                                Text(s.hasSkillMD ? "SKILL.md" : "missing SKILL.md")
                                    .font(.system(.caption, design: .monospaced))
                                    .foregroundStyle(.secondary)

                                Button("Open") { openURL(s.url) }
                                Button("Delete") { pendingDelete = s }
                            }
                            .padding(.vertical, 2)
                        }

                        if skills.isEmpty {
                            Text("(no skills)")
                                .font(.system(.caption, design: .monospaced))
                                .foregroundStyle(.secondary)
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(8)
                }
                .frame(minHeight: 240)
                .overlay {
                    RoundedRectangle(cornerRadius: 6)
                        .stroke(.quaternary, lineWidth: 1)
                }
            }

            if !statusText.isEmpty {
                Text(statusText)
                    .font(.system(.caption, design: .monospaced))
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }

            HStack {
                Button("New Skill") { showCreate = true }
                Spacer()
                Button("Close") { dismiss() }
            }
        }
        .padding(16)
        .frame(minWidth: 720, minHeight: 520)
        .task { await refresh() }
        .sheet(isPresented: $showCreate) {
            NewSkillSheetView(
                name: $newSkillName,
                errorText: $createErrorText,
                onCreate: { Task { await createSkill() } },
                onCancel: {
                    createErrorText = ""
                    showCreate = false
                }
            )
        }
        .alert("Delete skill?", isPresented: Binding(
            get: { pendingDelete != nil },
            set: { isPresented in
                if !isPresented {
                    pendingDelete = nil
                }
            }
        )) {
            Button("Delete", role: .destructive) {
                guard let s = pendingDelete else { return }
                pendingDelete = nil
                Task { await deleteSkill(s) }
            }
            Button("Cancel", role: .cancel) { pendingDelete = nil }
        } message: {
            Text(pendingDelete?.name ?? "")
        }
    }

    @MainActor
    private func refresh() async {
        statusText = ""
        do {
            try FileManager.default.createDirectory(at: skillsDirURL, withIntermediateDirectories: true)
            let urls = try FileManager.default.contentsOfDirectory(
                at: skillsDirURL,
                includingPropertiesForKeys: [.isDirectoryKey],
                options: [.skipsHiddenFiles]
            )
            var out: [SkillItem] = []
            for url in urls {
                guard let values = try? url.resourceValues(forKeys: [.isDirectoryKey]),
                      values.isDirectory == true
                else { continue }
                let name = url.lastPathComponent.trimmingCharacters(in: .whitespacesAndNewlines)
                if name.isEmpty || name.hasPrefix(".") || name == "__MACOSX" {
                    continue
                }
                if !isSafeSkillName(name) {
                    continue
                }
                let skillMDURL = url.appendingPathComponent("SKILL.md")
                out.append(SkillItem(
                    id: name,
                    name: name,
                    url: url,
                    hasSkillMD: FileManager.default.fileExists(atPath: skillMDURL.path)
                ))
            }
            out.sort { $0.name.lowercased() < $1.name.lowercased() }
            skills = out
        } catch {
            statusText = "Error: \(error)"
            skills = []
        }
    }

    @MainActor
    private func createSkill() async {
        createErrorText = ""
        let name = newSkillName.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else {
            createErrorText = "Name is required."
            return
        }
        guard !name.hasPrefix(".") && name != "__MACOSX" else {
            createErrorText = "Invalid skill name."
            return
        }
        guard isSafeSkillName(name) else {
            createErrorText = "Only letters, digits, '-', '_' and '.' are allowed."
            return
        }

        let dir = skillsDirURL.appendingPathComponent(name, isDirectory: true)
        if FileManager.default.fileExists(atPath: dir.path) {
            createErrorText = "Skill already exists."
            return
        }

        do {
            try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            let skillMDURL = dir.appendingPathComponent("SKILL.md")
            let content = "# \(name)\n\nDescribe what this skill does.\n"
            try content.write(to: skillMDURL, atomically: true, encoding: .utf8)
            newSkillName = ""
            showCreate = false
            await refresh()
        } catch {
            createErrorText = "Create error: \(error)"
        }
    }

    @MainActor
    private func deleteSkill(_ s: SkillItem) async {
        statusText = ""
        do {
            try FileManager.default.removeItem(at: s.url)
            await refresh()
        } catch {
            statusText = "Delete error: \(error)"
        }
    }

    private func openURL(_ url: URL) {
#if canImport(AppKit)
        NSWorkspace.shared.open(url)
#endif
    }

    private func isSafeSkillName(_ name: String) -> Bool {
        if name.isEmpty {
            return false
        }
        for r in name {
            switch r {
            case "a"..."z":
                continue
            case "A"..."Z":
                continue
            case "0"..."9":
                continue
            case "-", "_", ".":
                continue
            default:
                return false
            }
        }
        return true
    }
}

private struct NewSkillSheetView: View {
    @Binding var name: String
    @Binding var errorText: String
    let onCreate: () -> Void
    let onCancel: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("New Skill")
                .font(.title3)

            TextField("Skill name", text: $name)
                .textFieldStyle(.roundedBorder)

            Text("Allowed: letters, digits, '-', '_' and '.'")
                .font(.caption)
                .foregroundStyle(.secondary)

            if !errorText.isEmpty {
                Text(errorText)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .textSelection(.enabled)
            }

            HStack {
                Button("Cancel") { onCancel() }
                Spacer()
                Button("Create") { onCreate() }
                    .keyboardShortcut(.return, modifiers: [])
            }
        }
        .padding(16)
        .frame(minWidth: 420)
    }
}

