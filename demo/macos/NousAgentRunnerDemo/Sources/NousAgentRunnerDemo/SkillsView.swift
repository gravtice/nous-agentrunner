import SwiftUI
import NousAgentRunnerKit
#if canImport(AppKit)
import AppKit
#endif

private struct SkillItem: Identifiable, Hashable {
    let id: String
    let name: String
    let url: URL
    let hasSkillMD: Bool
    let sourceSummary: String?
}

struct SkillsView: View {
    @Environment(\.dismiss) private var dismiss

    @State private var statusText = ""
    @State private var skills: [SkillItem] = []
    @State private var showInstall = false
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
                            VStack(alignment: .leading, spacing: 4) {
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
                                if let src = s.sourceSummary, !src.isEmpty {
                                    Text(src)
                                        .font(.system(.caption, design: .monospaced))
                                        .foregroundStyle(.secondary)
                                        .lineLimit(1)
                                        .truncationMode(.middle)
                                        .textSelection(.enabled)
                                }
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
                Button("Install Skill") { showInstall = true }
                Spacer()
                Button("Close") { dismiss() }
            }
        }
        .padding(16)
        .frame(minWidth: 720, minHeight: 520)
        .task { await refresh() }
        .sheet(isPresented: $showInstall) {
            InstallSkillSheetView(
                onInstalled: {
                    Task { await refresh() }
                },
                onCancel: { showInstall = false }
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

    private func client() throws -> NousAgentRunnerClient {
        let runtime = try NousAgentRunnerRuntime.discover()
        return NousAgentRunnerClient(runtime: runtime)
    }

    @MainActor
    private func refresh() async {
        statusText = ""
        do {
            let c = try client()
            let resp = try await c.listSkills()
            let items = resp["skills"] as? [[String: Any]] ?? []
            var out: [SkillItem] = []
            out.reserveCapacity(items.count)
            for raw in items {
                guard let nameRaw = raw["name"] as? String else { continue }
                let name = nameRaw.trimmingCharacters(in: .whitespacesAndNewlines)
                if name.isEmpty || name.hasPrefix(".") || name == "__MACOSX" {
                    continue
                }
                let hasSkillMD = raw["has_skill_md"] as? Bool ?? false
                let url = skillsDirURL.appendingPathComponent(name, isDirectory: true)
                let src = summarizeSource(raw["source"] as? [String: Any])
                out.append(SkillItem(id: name, name: name, url: url, hasSkillMD: hasSkillMD, sourceSummary: src))
            }
            out.sort { $0.name.lowercased() < $1.name.lowercased() }
            skills = out
        } catch {
            statusText = "Error: \(error)"
            skills = []
        }
    }

    @MainActor
    private func deleteSkill(_ s: SkillItem) async {
        statusText = ""
        do {
            let c = try client()
            _ = try await c.deleteSkill(name: s.name)
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

    private func summarizeSource(_ raw: [String: Any]?) -> String? {
        guard let raw else { return nil }
        let source = (raw["source"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let url = (raw["url"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let ref = (raw["ref"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let subpath = (raw["subpath"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let commit = (raw["commit"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let parts: [String] = [
            source.isEmpty ? nil : "source=\(source)",
            url.isEmpty ? nil : "url=\(url)",
            ref.isEmpty ? nil : "ref=\(ref)",
            subpath.isEmpty ? nil : "subpath=\(subpath)",
            commit.isEmpty ? nil : "commit=\(commit)",
        ].compactMap { $0 }
        return parts.isEmpty ? nil : parts.joined(separator: " ")
    }
}

private struct DiscoveredSkillItem: Identifiable, Hashable {
    let id: String
    let installName: String
    let name: String
    let description: String
    let skillPath: String
}

private struct InstallSkillSheetView: View {
    @State private var source = ""
    @State private var ref = ""
    @State private var subpath = ""
    @State private var replace = false
    @State private var statusText = ""
    @State private var errorText = ""
    @State private var discovered: [DiscoveredSkillItem] = []
    @State private var selected: Set<String> = []
    @State private var isDiscovering = false
    @State private var isInstalling = false

    let onInstalled: () -> Void
    let onCancel: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Install Skill")
                .font(.title3)

            TextField("Source (e.g. remotion-dev/skills)", text: $source)
                .textFieldStyle(.roundedBorder)

            HStack {
                TextField("Ref (optional)", text: $ref)
                    .textFieldStyle(.roundedBorder)
                TextField("Subpath (optional)", text: $subpath)
                    .textFieldStyle(.roundedBorder)
            }

            Toggle("Replace existing skills", isOn: $replace)

            GroupBox("Discover") {
                VStack(alignment: .leading, spacing: 8) {
                    HStack {
                        Button(isDiscovering ? "Discovering..." : "Discover") { Task { await discover() } }
                            .disabled(isDiscovering || isInstalling)
                        Spacer()
                        Button("Select All") {
                            selected = Set(discovered.map { $0.installName })
                        }
                        .disabled(discovered.isEmpty || isInstalling)
                        Button("Clear") { selected = [] }
                            .disabled(discovered.isEmpty || isInstalling)
                    }

                    ScrollView {
                        VStack(alignment: .leading, spacing: 8) {
                            ForEach(discovered) { s in
                                Toggle(isOn: Binding(
                                    get: { selected.contains(s.installName) },
                                    set: { checked in
                                        if checked {
                                            selected.insert(s.installName)
                                        } else {
                                            selected.remove(s.installName)
                                        }
                                    }
                                )) {
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(s.installName)
                                            .font(.system(.body, design: .monospaced))
                                        if !s.name.isEmpty {
                                            Text(s.name)
                                                .font(.system(.caption, design: .monospaced))
                                                .foregroundStyle(.secondary)
                                        }
                                        if !s.description.isEmpty {
                                            Text(s.description)
                                                .font(.system(.caption, design: .monospaced))
                                                .foregroundStyle(.secondary)
                                                .lineLimit(2)
                                        }
                                        if !s.skillPath.isEmpty {
                                            Text("path: \(s.skillPath)")
                                                .font(.system(.caption, design: .monospaced))
                                                .foregroundStyle(.secondary)
                                                .lineLimit(1)
                                                .truncationMode(.middle)
                                        }
                                    }
                                }
                            }
                            if discovered.isEmpty {
                                Text("(no discovered skills yet)")
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
            }

            if !errorText.isEmpty {
                Text(errorText)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .textSelection(.enabled)
            }
            if !statusText.isEmpty {
                Text(statusText)
                    .font(.system(.caption, design: .monospaced))
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }

            HStack {
                Button("Cancel") { onCancel() }
                Spacer()
                Button(isInstalling ? "Installing..." : "Install Selected") { Task { await install() } }
                    .disabled(isDiscovering || isInstalling || selected.isEmpty)
                    .keyboardShortcut(.return, modifiers: [])
            }
        }
        .padding(16)
        .frame(minWidth: 720, minHeight: 520)
    }

    private func client() throws -> NousAgentRunnerClient {
        let runtime = try NousAgentRunnerRuntime.discover()
        return NousAgentRunnerClient(runtime: runtime)
    }

    @MainActor
    private func discover() async {
        errorText = ""
        statusText = ""
        let trimmed = source.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            errorText = "Source is required."
            return
        }
        isDiscovering = true
        defer { isDiscovering = false }

        do {
            let c = try client()
            let resp = try await c.discoverSkills(
                source: trimmed,
                ref: ref.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : ref,
                subpath: subpath.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : subpath
            )
            let skills = resp["skills"] as? [[String: Any]] ?? []
            var out: [DiscoveredSkillItem] = []
            out.reserveCapacity(skills.count)
            for raw in skills {
                let installName = (raw["install_name"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                if installName.isEmpty { continue }
                let name = (raw["name"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                let desc = (raw["description"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                let skillPath = (raw["skill_path"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                out.append(DiscoveredSkillItem(id: installName, installName: installName, name: name, description: desc, skillPath: skillPath))
            }
            out.sort { $0.installName.lowercased() < $1.installName.lowercased() }
            discovered = out
            selected = []
            if let commit = resp["commit"] as? String, !commit.isEmpty {
                statusText = "commit: \(commit)"
            } else {
                statusText = "discovered: \(out.count)"
            }
        } catch {
            errorText = "Discover error: \(error)"
        }
    }

    @MainActor
    private func install() async {
        errorText = ""
        statusText = ""
        let trimmed = source.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            errorText = "Source is required."
            return
        }
        let skills = selected.sorted { $0.lowercased() < $1.lowercased() }
        guard !skills.isEmpty else {
            errorText = "Select at least one skill to install."
            return
        }

        isInstalling = true
        defer { isInstalling = false }

        do {
            let c = try client()
            let resp = try await c.installSkills(
                source: trimmed,
                ref: ref.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : ref,
                subpath: subpath.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : subpath,
                skills: skills,
                replace: replace
            )
            let installed = resp["installed"] as? [String] ?? []
            statusText = "installed: \(installed.joined(separator: ", "))"
            onInstalled()
            onCancel()
        } catch {
            errorText = "Install error: \(error)"
        }
    }
}
