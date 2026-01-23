import SwiftUI
import NousAgentRunnerKit
import UniformTypeIdentifiers
#if canImport(AppKit)
import AppKit
#endif

struct ContentView: View {
    @State private var statusText = "Not loaded"
    @State private var imageRef = "docker.io/gravtice/nous-claude-agent-service:0.1.1"
    @State private var systemPrompt = "You are a helpful agent."
    @State private var rwMount = ""
    @State private var selectedWorkDirURL: URL?
    @State private var showWorkDirPicker = false
    @AppStorage("nous.demo.service_env") private var serviceEnvText = ""
    @State private var showSettings = false
    @State private var serviceID: String?
    @State private var workDirPath: String?
    @State private var chatInput = ""
    @State private var chatOutput = ""
    @State private var selectedImageURL: URL?
    @State private var showImagePicker = false
    @State private var isSending = false

    @State private var wsTask: URLSessionWebSocketTask?
    @State private var wsGeneration = 0
    @State private var daemon: NousAgentRunnerDaemon?

    private var hasMessage: Bool {
        selectedImageURL != nil || !chatInput.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }
    private var canSend: Bool { wsTask != nil && hasMessage && !isSending }

    private func isBenignWSError(_ err: Error) -> Bool {
        let e = err as NSError
        if e.domain == NSPOSIXErrorDomain && e.code == 57 { return true } // ENOTCONN
        if e.domain == NSURLErrorDomain && e.code == NSURLErrorCancelled { return true }
        return false
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Nous Agent Runner Demo")
                .font(.title2)

            Text(statusText)
                .font(.system(.body, design: .monospaced))
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)

            HStack {
                Button("Refresh Status") { Task { await refreshStatus() } }
                Button("Restart VM") { Task { await restartVM() } }
                Button("Create Claude Service") { Task { await createService() } }
                Button("Settings") { showSettings = true }
                Button("Open Logs") { openRunnerLogs() }
                Button("Open VM Logs") { openVMLogs() }
                if let serviceID {
                    Button("Connect WS") { connectWS(serviceID: serviceID) }
                }
            }

            GroupBox("Service") {
                VStack(alignment: .leading) {
                    TextField("image_ref", text: $imageRef)
                    TextField("rw_mount (optional)", text: $rwMount)
                    TextField("system_prompt", text: $systemPrompt)
                    HStack {
                        Button("Pick Work Dir") { showWorkDirPicker = true }
                        if let url = selectedWorkDirURL {
                            Text("selected_work_dir: \(url.path)")
                                .font(.system(.caption, design: .monospaced))
                                .textSelection(.enabled)
                                .lineLimit(1)
                                .truncationMode(.middle)
                            Button("Open") { openWorkDir(url.path) }
                            Button("Clear") { selectedWorkDirURL = nil }
                        } else {
                            Text("selected_work_dir: (auto)")
                                .font(.system(.caption, design: .monospaced))
                                .foregroundStyle(.secondary)
                        }
                    }
                    if let workDirPath {
                        HStack {
                            Text("work_dir: \(workDirPath)")
                                .font(.system(.caption, design: .monospaced))
                                .textSelection(.enabled)
                            Button("Open Work Dir") { openWorkDir(workDirPath) }
                        }
                    }
                    if let serviceID {
                        Text("service_id: \(serviceID)")
                            .font(.system(.body, design: .monospaced))
                    }
                }
            }

            GroupBox("Chat") {
                VStack(alignment: .leading) {
                    ScrollView {
                        Text(chatOutput)
                            .font(.system(.body, design: .monospaced))
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(8)
                    }
                    .frame(minHeight: 200)
                    .overlay {
                        RoundedRectangle(cornerRadius: 6)
                            .stroke(.quaternary, lineWidth: 1)
                    }

                    HStack {
                        Button("Pick Image") { showImagePicker = true }
                        if let url = selectedImageURL {
                            Text(url.lastPathComponent)
                                .font(.system(.caption, design: .monospaced))
                            Button("Clear") { selectedImageURL = nil }
                        }
                        TextField("message", text: $chatInput)
                            .submitLabel(.send)
                            .onSubmit { sendChat() }
                        Button("Send") { sendChat() }
                            .keyboardShortcut(.return, modifiers: [])
                            .disabled(!canSend)
                    }
                }
            }

            Spacer()
        }
        .padding(16)
        .frame(minWidth: 700, minHeight: 600)
        .sheet(isPresented: $showSettings) {
            SettingsView(serviceEnvText: $serviceEnvText)
        }
        .fileImporter(isPresented: $showImagePicker, allowedContentTypes: allowedImageTypes(), allowsMultipleSelection: false) { result in
            switch result {
            case .success(let urls):
                guard let url = urls.first else { return }
                selectedImageURL = url
                statusText = "Selected image: \(url.lastPathComponent)"
            case .failure(let err):
                statusText = "Pick image error: \(err)"
            }
        }
        .fileImporter(isPresented: $showWorkDirPicker, allowedContentTypes: [.folder], allowsMultipleSelection: false) { result in
            switch result {
            case .success(let urls):
                guard let url = urls.first else { return }
                selectedWorkDirURL = url
                statusText = "Selected work dir: \(url.path)"
            case .failure(let err):
                statusText = "Pick work dir error: \(err)"
            }
        }
        .task {
            await ensureRunnerRunning()
            await refreshStatus()
        }
    }

    private func client() throws -> NousAgentRunnerClient {
        let runtime = try NousAgentRunnerRuntime.discover()
        return NousAgentRunnerClient(runtime: runtime)
    }

    private func openRunnerLogs() {
#if canImport(AppKit)
        let instanceID = loadInstanceIDFromBundle()
        let url = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library")
            .appendingPathComponent("Application Support")
            .appendingPathComponent("NousAgentRunner")
            .appendingPathComponent(instanceID)
            .appendingPathComponent("runnerd.log")
        NSWorkspace.shared.activateFileViewerSelecting([url])
#endif
    }

    private func openVMLogs() {
#if canImport(AppKit)
        let instanceID = loadInstanceIDFromBundle()
        let url = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library")
            .appendingPathComponent("Caches")
            .appendingPathComponent("NousAgentRunner")
            .appendingPathComponent(instanceID)
            .appendingPathComponent("lima")
        NSWorkspace.shared.open(url)
#endif
    }

    private func openWorkDir(_ path: String) {
#if canImport(AppKit)
        let url = URL(fileURLWithPath: path, isDirectory: true)
        NSWorkspace.shared.open(url)
#endif
    }

    @MainActor
    private func ensureRunnerRunning() async {
        do {
            if daemon == nil {
                daemon = try NousAgentRunnerDaemon()
            }
            _ = try await daemon?.ensureRunning()
        } catch {
            statusText = "Runner error: \(error)"
        }
    }

    @MainActor
    private func refreshStatus() async {
        do {
            let c = try client()
            let status = try await c.getSystemStatus()
            statusText = "\(status)"
        } catch {
            statusText = "Error: \(error)"
        }
    }

    @MainActor
    private func restartVM() async {
        statusText = "Restarting VM (may take a few minutes on first run)..."
        do {
            let c = try client()
            _ = try await c.restartVM()
            await refreshStatus()
        } catch {
            statusText = "Error: \(error)"
        }
    }

    @MainActor
    private func createService() async {
        statusText = "Creating Claude service (may take a few minutes on first run)..."
        do {
            let c = try client()
            let rw = rwMount.trimmingCharacters(in: .whitespacesAndNewlines)
            var mounts = rw.isEmpty ? [] : [rw]
            let env = try parseEnvText(serviceEnvText)

            let workDir: String
            if let url = selectedWorkDirURL {
                workDir = try prepareSelectedWorkDir(url: url)
            } else {
                workDir = try await allocateWorkDir(client: c)
            }
            workDirPath = workDir
            mounts.append(workDir)

            let resp = try await c.createClaudeService(imageRef: imageRef, rwMounts: mounts, env: env, serviceConfig: [
                "system_prompt": systemPrompt,
                "cwd": workDir,
            ])
            serviceID = resp["service_id"] as? String
            if let serviceID {
                statusText = "Created: \(serviceID) (connecting WS...)"
            } else {
                statusText = "Created service"
            }
            chatOutput = ""
            if let serviceID {
                connectWS(serviceID: serviceID)
            }
        } catch {
            statusText = "Error: \(error)"
        }
    }

    private func allocateWorkDir(client: NousAgentRunnerClient) async throws -> String {
        let paths = try await client.getSystemPaths()
        guard let tmpDir = paths["default_temp_dir"] as? String, !tmpDir.isEmpty else {
            throw NousAgentRunnerError.invalidConfig("missing default_temp_dir")
        }
        let base = URL(fileURLWithPath: tmpDir, isDirectory: true)
            .appendingPathComponent("Work", isDirectory: true)
        let dir = base.appendingPathComponent("claude-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir.path
    }

    private func prepareSelectedWorkDir(url: URL) throws -> String {
        let needsStop = url.startAccessingSecurityScopedResource()
        defer {
            if needsStop {
                url.stopAccessingSecurityScopedResource()
            }
        }

        var isDir: ObjCBool = false
        guard FileManager.default.fileExists(atPath: url.path, isDirectory: &isDir), isDir.boolValue else {
            throw NousAgentRunnerError.invalidConfig("selected work dir is not a directory: \(url.path)")
        }
        return url.path
    }

    private func connectWS(serviceID: String) {
        do {
            wsTask?.cancel(with: .goingAway, reason: nil)
            wsTask = nil
            wsGeneration += 1
            let generation = wsGeneration
            let c = try client()
            let ws = try c.openChatWebSocket(serviceID: serviceID)
            wsTask = ws
            ws.resume()
            receiveLoop(wsTask: ws, generation: generation)
        } catch {
            statusText = "Error: \(error)"
        }
    }

    private func receiveLoop(wsTask: URLSessionWebSocketTask, generation: Int) {
        wsTask.receive { result in
            switch result {
            case .failure(let err):
                DispatchQueue.main.async {
                    guard generation == wsGeneration else { return }
                    if isBenignWSError(err) {
                        statusText = "WS disconnected"
                    } else {
                        statusText = "WS error: \(err)"
                    }
                }
            case .success(let msg):
                switch msg {
                case .string(let s):
                    DispatchQueue.main.async {
                        guard generation == wsGeneration else { return }
                        guard let data = s.data(using: .utf8),
                              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                              let type = obj["type"] as? String
                        else {
                            chatOutput += s + "\n"
                            return
                        }

                        switch type {
                        case "session.started":
                            statusText = "WS connected"
                        case "response.delta":
                            if let t = obj["text"] as? String {
                                chatOutput += t
                            }
                        case "response.final":
                            if let contents = obj["contents"] as? [[String: Any]] {
                                let text = contents.compactMap { c -> String? in
                                    guard (c["kind"] as? String) == "text" else { return nil }
                                    return c["text"] as? String
                                }.joined()
                                if !text.isEmpty {
                                    chatOutput += text
                                }
                            }
                            chatOutput += "\n"
                        case "error":
                            let code = obj["code"] as? String ?? "ERROR"
                            let msg = obj["message"] as? String ?? ""
                            chatOutput += "\n[\(code)] \(msg)\n"
                        case "done":
                            break
                        default:
                            chatOutput += s + "\n"
                        }
                    }
                default:
                    break
                }
                receiveLoop(wsTask: wsTask, generation: generation)
            }
        }
    }

    private func sendChat() {
        guard let wsTask else {
            statusText = "WS not connected (click Connect WS)"
            return
        }
        if !hasMessage || isSending {
            return
        }
        isSending = true
        let text = chatInput.trimmingCharacters(in: .whitespacesAndNewlines)
        chatInput = ""
        Task {
            do {
                defer { Task { @MainActor in isSending = false } }
                var contents: [[String: Any]] = []
                if let url = selectedImageURL {
                    let staged = try await stageImageIntoSharedTmp(url: url)
                    let mime = inferMimeType(url: staged) ?? "application/octet-stream"
                    contents.append([
                        "kind": "image",
                        "source": [
                            "type": "path",
                            "path": staged.path,
                            "mime": mime,
                        ],
                    ])
                }
                if !text.isEmpty {
                    contents.append(["kind": "text", "text": text])
                }
                if contents.isEmpty {
                    return
                }
                let payload: [String: Any] = ["type": "input", "contents": contents]
                let data = try JSONSerialization.data(withJSONObject: payload)
                guard let json = String(data: data, encoding: .utf8) else { return }
                wsTask.send(.string(json)) { err in
                    if let err {
                        DispatchQueue.main.async { statusText = "WS send error: \(err)" }
                    }
                }
                DispatchQueue.main.async { selectedImageURL = nil }
            } catch {
                await MainActor.run { isSending = false }
                DispatchQueue.main.async { statusText = "Send error: \(error)" }
            }
        }
    }

    private func allowedImageTypes() -> [UTType] {
        return [.image]
    }

    private func inferMimeType(url: URL) -> String? {
        if let t = UTType(filenameExtension: url.pathExtension) {
            return t.preferredMIMEType
        }
        return nil
    }

    private func stageImageIntoSharedTmp(url: URL) async throws -> URL {
        let needsStop = url.startAccessingSecurityScopedResource()
        defer {
            if needsStop {
                url.stopAccessingSecurityScopedResource()
            }
        }
        let c = try client()
        let paths = try await c.getSystemPaths()
        guard let tmpDir = paths["default_temp_dir"] as? String, !tmpDir.isEmpty else {
            throw NousAgentRunnerError.invalidConfig("missing default_temp_dir")
        }
        let fm = FileManager.default
        let imagesDir = URL(fileURLWithPath: tmpDir, isDirectory: true).appendingPathComponent("Images", isDirectory: true)
        try fm.createDirectory(at: imagesDir, withIntermediateDirectories: true)
        let ext = url.pathExtension.isEmpty ? "img" : url.pathExtension
        let dst = imagesDir.appendingPathComponent("img-\(UUID().uuidString).\(ext)")
        if fm.fileExists(atPath: dst.path) {
            try fm.removeItem(at: dst)
        }
        try fm.copyItem(at: url, to: dst)
        return dst
    }

    private func parseEnvText(_ text: String) throws -> [String: String] {
        var out: [String: String] = [:]
        let lines = text.split(separator: "\n", omittingEmptySubsequences: false)
        for (idx, rawLine) in lines.enumerated() {
            var line = String(rawLine).trimmingCharacters(in: .whitespacesAndNewlines)
            if line.isEmpty || line.hasPrefix("#") {
                continue
            }
            if line.hasPrefix("export ") {
                line = String(line.dropFirst("export ".count)).trimmingCharacters(in: .whitespacesAndNewlines)
            }

            guard let eq = line.firstIndex(of: "=") else {
                throw NousAgentRunnerError.invalidConfig("env line \(idx + 1): missing '='")
            }
            let key = String(line[..<eq]).trimmingCharacters(in: .whitespacesAndNewlines)
            var value = String(line[line.index(after: eq)...]).trimmingCharacters(in: .whitespacesAndNewlines)
            if (value.hasPrefix("\"") && value.hasSuffix("\"")) || (value.hasPrefix("'") && value.hasSuffix("'")) {
                value = String(value.dropFirst().dropLast())
            }

            if key.isEmpty {
                throw NousAgentRunnerError.invalidConfig("env line \(idx + 1): empty key")
            }
            if key.hasPrefix("NOUS_") {
                throw NousAgentRunnerError.invalidConfig("env key is reserved: \(key)")
            }
            if !isValidEnvKey(key) {
                throw NousAgentRunnerError.invalidConfig("invalid env key: \(key)")
            }
            if out[key] != nil {
                throw NousAgentRunnerError.invalidConfig("duplicate env key: \(key)")
            }
            out[key] = value
        }
        return out
    }

    private func isValidEnvKey(_ key: String) -> Bool {
        guard let first = key.utf8.first else { return false }
        if first >= 48 && first <= 57 { return false } // 0-9
        for (i, b) in key.utf8.enumerated() {
            if b == 95 { continue } // _
            if b >= 65 && b <= 90 { continue } // A-Z
            if b >= 97 && b <= 122 { continue } // a-z
            if i > 0 && b >= 48 && b <= 57 { continue } // 0-9
            return false
        }
        return true
    }

    private func loadInstanceIDFromBundle() -> String {
        guard let url = Bundle.main.url(forResource: "NousAgentRunnerConfig", withExtension: "json"),
              let data = try? Data(contentsOf: url),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else { return "default" }
        if let v = obj["instance_id"] as? String, !v.isEmpty {
            return v
        }
        return "default"
    }
}
