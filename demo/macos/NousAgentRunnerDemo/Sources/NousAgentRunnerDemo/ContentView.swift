import SwiftUI
import NousAgentRunnerKit
import UniformTypeIdentifiers
#if canImport(AppKit)
import AppKit
#endif

private struct AskOption: Identifiable, Hashable {
    let id: Int
    let label: String
    let description: String
}

private struct AskQuestion: Identifiable, Hashable {
    let id: Int
    let header: String
    let question: String
    let options: [AskOption]
    let multiSelect: Bool
}

private struct AskRequest: Identifiable {
    let id: String // ask_id
    let questions: [AskQuestion]
}

private struct AskSheetView: View {
    let ask: AskRequest
    let onSubmit: ([String: String]) -> Void
    let onCancel: () -> Void

    @State private var customAnswerByQuestion: [String: String] = [:]
    @State private var selectedIndexByQuestion: [String: Int] = [:]
    @State private var selectedIndicesByQuestion: [String: Set<Int>] = [:]

    private func bindingForSingle(question: AskQuestion) -> Binding<Int> {
        Binding(
            get: { selectedIndexByQuestion[question.question] ?? 0 },
            set: { selectedIndexByQuestion[question.question] = $0 }
        )
    }

    private func bindingForMulti(question: AskQuestion, index: Int) -> Binding<Bool> {
        Binding(
            get: { selectedIndicesByQuestion[question.question, default: []].contains(index) },
            set: { isOn in
                var set = selectedIndicesByQuestion[question.question, default: []]
                if isOn {
                    set.insert(index)
                } else {
                    set.remove(index)
                }
                selectedIndicesByQuestion[question.question] = set
            }
        )
    }

    private func buildAnswers() -> [String: String] {
        var out: [String: String] = [:]
        for q in ask.questions {
            let custom = (customAnswerByQuestion[q.question] ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            if !custom.isEmpty {
                out[q.question] = custom
                continue
            }
            if q.multiSelect {
                let indices = selectedIndicesByQuestion[q.question, default: []].sorted()
                let labels: [String] = indices.compactMap { i -> String? in
                    guard i >= 0 && i < q.options.count else { return nil }
                    return q.options[i].label
                }
                out[q.question] = labels.joined(separator: ", ")
            } else {
                let idx = selectedIndexByQuestion[q.question] ?? 0
                if idx >= 0 && idx < q.options.count {
                    out[q.question] = q.options[idx].label
                } else {
                    out[q.question] = ""
                }
            }
        }
        return out
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Agent Ask")
                .font(.title3)
            Text("ask_id: \(ask.id)")
                .font(.system(.caption, design: .monospaced))
                .foregroundStyle(.secondary)
                .textSelection(.enabled)

            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    ForEach(ask.questions) { q in
                        VStack(alignment: .leading, spacing: 8) {
                            Text("\(q.header): \(q.question)")
                                .font(.headline)

                            if q.options.isEmpty {
                                Text("No options provided.")
                                    .foregroundStyle(.secondary)
                            } else if q.multiSelect {
                                VStack(alignment: .leading, spacing: 6) {
                                    ForEach(q.options.indices, id: \.self) { i in
                                        let opt = q.options[i]
                                        Toggle(isOn: bindingForMulti(question: q, index: i)) {
                                            Text("\(opt.label) — \(opt.description)")
                                                .font(.system(.body, design: .monospaced))
                                        }
                                    }
                                }
                            } else {
                                Picker("Options", selection: bindingForSingle(question: q)) {
                                    ForEach(q.options.indices, id: \.self) { i in
                                        let opt = q.options[i]
                                        Text("\(opt.label) — \(opt.description)").tag(i)
                                    }
                                }
                                .pickerStyle(.radioGroup)
                            }

                            TextField("Custom answer (optional)", text: Binding(
                                get: { customAnswerByQuestion[q.question] ?? "" },
                                set: { customAnswerByQuestion[q.question] = $0 }
                            ))
                            .textFieldStyle(.roundedBorder)
                        }
                        .padding(8)
                        .overlay {
                            RoundedRectangle(cornerRadius: 6)
                                .stroke(.quaternary, lineWidth: 1)
                        }
                    }
                }
                .padding(.vertical, 4)
            }

            HStack {
                Button("Cancel") { onCancel() }
                Spacer()
                Button("Submit") { onSubmit(buildAnswers()) }
                    .keyboardShortcut(.return, modifiers: [])
            }
        }
        .padding(16)
        .frame(minWidth: 720, minHeight: 520)
        .onAppear {
            for q in ask.questions {
                if !q.multiSelect, selectedIndexByQuestion[q.question] == nil {
                    selectedIndexByQuestion[q.question] = 0
                }
            }
        }
    }
}

struct ContentView: View {
    @State private var statusText = "Not loaded"
    @State private var imageRef = "docker.io/gravtice/nous-claude-agent-service:0.1.1"

    private enum SystemPromptMode: String, CaseIterable, Identifiable {
        case builtin
        case custom

        var id: String { rawValue }
    }

    private static let sampleMcpServersJSON =
        """
        {
          "genaisdk": {
            "type": "http",
            "url": "https://happy.zengjice.com:7001/mcp",
            "headers": {
              "Authorization": "Bearer <token>"
            }
          }
        }
        """

    @State private var systemPromptMode: SystemPromptMode = .custom
    @State private var systemPromptCustom = "You are a helpful agent."
    @State private var systemPromptAppend = ""
    @State private var rwMount = ""
    @State private var selectedWorkDirURL: URL?
    @State private var showWorkDirPicker = false
    @AppStorage("nous.demo.service_env") private var serviceEnvText = ""
    @State private var services: [[String: Any]] = []
    @State private var builtinTools: [String] = []
    @State private var restrictTools = false
    @State private var allowedTools = Set<String>()
    @State private var extraAllowedToolsText = ""
    @State private var mcpServersText = ""
    @State private var agentsText = ""
    @State private var showSettings = false
    @State private var serviceID: String?
    @State private var workDirPath: String?
    @State private var chatInput = ""
    @State private var chatOutput = ""
    @State private var debugThinking = ""
    @State private var debugEvents = ""
    @State private var showDebug = false
    @State private var pendingAsk: AskRequest?
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

    private func bindingForAllowedTool(_ name: String) -> Binding<Bool> {
        Binding(
            get: { allowedTools.contains(name) },
            set: { isOn in
                if isOn {
                    allowedTools.insert(name)
                } else {
                    allowedTools.remove(name)
                }
            }
        )
    }

    private func parseCommaNewlineList(_ text: String) -> [String] {
        let raw = text
            .split(whereSeparator: { $0 == "," || $0 == "\n" || $0 == "\r" })
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
        var seen = Set<String>()
        var out: [String] = []
        for s in raw where !seen.contains(s) {
            seen.insert(s)
            out.append(s)
        }
        return out
    }

    private func parseJSONObjectText(_ text: String) throws -> [String: Any] {
        guard let data = text.data(using: .utf8) else {
            throw NousAgentRunnerError.invalidConfig("invalid utf-8")
        }
        let obj = try JSONSerialization.jsonObject(with: data)
        guard let dict = obj as? [String: Any] else {
            throw NousAgentRunnerError.invalidConfig("json must be an object")
        }
        return dict
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
                Button("Refresh Services") { Task { await refreshServices() } }
                Button("Restart VM") { Task { await restartVM() } }
                Button("Create Service") { Task { await createService() } }
                Button("Settings") { showSettings = true }
                Button("Open Logs") { openRunnerLogs() }
                Button("Open VM Logs") { openVMLogs() }
                if let serviceID {
                    Button("Connect WS") { connectWS(serviceID: serviceID) }
                    Button("Delete Service") { Task { await deleteService(serviceID: serviceID) } }
                }
            }

            GroupBox("Services") {
                VStack(alignment: .leading, spacing: 8) {
                    HStack {
                        Button("Load Builtin Tools") { Task { await refreshBuiltinTools() } }
                        Spacer()
                        Text("count: \(services.count)")
                            .font(.system(.caption, design: .monospaced))
                            .foregroundStyle(.secondary)
                    }
                    ScrollView {
                        LazyVStack(alignment: .leading, spacing: 6) {
                            ForEach(services.indices, id: \.self) { i in
                                let svc = services[i]
                                let sid = svc["service_id"] as? String ?? ""
                                let typ = svc["type"] as? String ?? ""
                                let state = svc["state"] as? String ?? ""
                                let createdAt = svc["created_at"] as? String ?? ""
                                HStack(spacing: 10) {
                                    Text(sid.isEmpty ? "(missing service_id)" : sid)
                                        .font(.system(.caption, design: .monospaced))
                                        .textSelection(.enabled)
                                        .lineLimit(1)
                                        .truncationMode(.middle)
                                    Spacer()
                                    Text("\(typ) \(state)")
                                        .font(.system(.caption, design: .monospaced))
                                        .foregroundStyle(.secondary)
                                    if !createdAt.isEmpty {
                                        Text(createdAt)
                                            .font(.system(.caption2, design: .monospaced))
                                            .foregroundStyle(.secondary)
                                            .lineLimit(1)
                                    }
                                    Button("Use") { serviceID = sid }
                                        .disabled(sid.isEmpty)
                                    Button("Connect") { connectWS(serviceID: sid) }
                                        .disabled(sid.isEmpty)
                                    Button("Delete") { Task { await deleteService(serviceID: sid) } }
                                        .disabled(sid.isEmpty)
                                }
                            }
                            if services.isEmpty {
                                Text("(no services)")
                                    .font(.system(.caption, design: .monospaced))
                                    .foregroundStyle(.secondary)
                            }
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .frame(minHeight: 120)
                }
            }

            GroupBox("Create Service") {
                VStack(alignment: .leading, spacing: 10) {
                    TextField("image_ref", text: $imageRef)
                    TextField("rw_mount (optional)", text: $rwMount)

                    HStack(alignment: .top) {
                        Text("system_prompt")
                            .font(.system(.caption, design: .monospaced))
                            .foregroundStyle(.secondary)
                        Picker("", selection: $systemPromptMode) {
                            Text("builtin (claude_code)").tag(SystemPromptMode.builtin)
                            Text("custom").tag(SystemPromptMode.custom)
                        }
                        .pickerStyle(.segmented)
                    }

                    if systemPromptMode == .custom {
                        TextEditor(text: $systemPromptCustom)
                            .font(.system(.caption, design: .monospaced))
                            .frame(minHeight: 70)
                            .overlay {
                                RoundedRectangle(cornerRadius: 6)
                                    .stroke(.quaternary, lineWidth: 1)
                            }
                    } else {
                        TextField("append_system_prompt (optional)", text: $systemPromptAppend)
                            .font(.system(.caption, design: .monospaced))
                    }

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

                    VStack(alignment: .leading, spacing: 6) {
                        HStack {
                            Text("mcp_servers (json/path)")
                                .font(.system(.caption, design: .monospaced))
                                .foregroundStyle(.secondary)
                            Spacer()
                            Button("Sample") { mcpServersText = Self.sampleMcpServersJSON }
                            Button("Clear") { mcpServersText = "" }
                        }
                        TextEditor(text: $mcpServersText)
                            .font(.system(.caption, design: .monospaced))
                            .frame(minHeight: 90)
                            .overlay {
                                RoundedRectangle(cornerRadius: 6)
                                    .stroke(.quaternary, lineWidth: 1)
                            }
                    }

                    VStack(alignment: .leading, spacing: 6) {
                        Toggle("restrict allowed_tools", isOn: $restrictTools)
                        if restrictTools {
                            if builtinTools.isEmpty {
                                Text("builtin_tools not loaded (click Load Builtin Tools)")
                                    .font(.system(.caption, design: .monospaced))
                                    .foregroundStyle(.secondary)
                            } else {
                                ScrollView {
                                    LazyVStack(alignment: .leading, spacing: 4) {
                                        ForEach(builtinTools, id: \.self) { name in
                                            Toggle(isOn: bindingForAllowedTool(name)) {
                                                Text(name)
                                                    .font(.system(.caption, design: .monospaced))
                                            }
                                        }
                                    }
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                }
                                .frame(minHeight: 80)
                            }
                            TextField("extra allowed_tools (comma/newline separated)", text: $extraAllowedToolsText)
                                .font(.system(.caption, design: .monospaced))
                        }
                    }

                    VStack(alignment: .leading, spacing: 6) {
                        Text("agents (JSON object; optional)")
                            .font(.system(.caption, design: .monospaced))
                            .foregroundStyle(.secondary)
                        TextEditor(text: $agentsText)
                            .font(.system(.caption, design: .monospaced))
                            .frame(minHeight: 90)
                            .overlay {
                                RoundedRectangle(cornerRadius: 6)
                                    .stroke(.quaternary, lineWidth: 1)
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

                    DisclosureGroup("Debug", isExpanded: $showDebug) {
                        VStack(alignment: .leading, spacing: 8) {
                            Text("Thinking")
                                .font(.system(.caption, design: .monospaced))
                                .foregroundStyle(.secondary)
                            ScrollView {
                                Text(debugThinking)
                                    .font(.system(.caption, design: .monospaced))
                                    .textSelection(.enabled)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                    .padding(8)
                            }
                            .frame(minHeight: 80)
                            .overlay {
                                RoundedRectangle(cornerRadius: 6)
                                    .stroke(.quaternary, lineWidth: 1)
                            }

                            Text("Events")
                                .font(.system(.caption, design: .monospaced))
                                .foregroundStyle(.secondary)
                            ScrollView {
                                Text(debugEvents)
                                    .font(.system(.caption, design: .monospaced))
                                    .textSelection(.enabled)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                    .padding(8)
                            }
                            .frame(minHeight: 80)
                            .overlay {
                                RoundedRectangle(cornerRadius: 6)
                                    .stroke(.quaternary, lineWidth: 1)
                            }
                        }
                        .padding(.top, 4)
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
        .sheet(item: $pendingAsk) { ask in
            AskSheetView(
                ask: ask,
                onSubmit: { answers in sendAskAnswer(askID: ask.id, answers: answers) },
                onCancel: { pendingAsk = nil }
            )
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
            await refreshServices()
            await refreshBuiltinTools()
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
    private func refreshServices() async {
        do {
            let c = try client()
            let resp = try await c.listServices()
            services = resp["services"] as? [[String: Any]] ?? []
        } catch {
            statusText = "Error: \(error)"
        }
    }

    @MainActor
    private func refreshBuiltinTools() async {
        do {
            let c = try client()
            let resp = try await c.getBuiltinTools(serviceType: "claude")
            builtinTools = resp["builtin_tools"] as? [String] ?? []
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
    private func deleteService(serviceID: String) async {
        let sid = serviceID.trimmingCharacters(in: .whitespacesAndNewlines)
        if sid.isEmpty { return }
        do {
            let c = try client()
            _ = try await c.deleteService(serviceID: sid)
            if self.serviceID == sid {
                self.serviceID = nil
            }
            await refreshServices()
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

            var serviceConfig: [String: Any] = [
                "cwd": workDir,
            ]

            switch systemPromptMode {
            case .builtin:
                var preset: [String: Any] = ["type": "preset", "preset": "claude_code"]
                let append = systemPromptAppend.trimmingCharacters(in: .whitespacesAndNewlines)
                if !append.isEmpty {
                    preset["append"] = append
                }
                serviceConfig["system_prompt"] = preset
            case .custom:
                serviceConfig["system_prompt"] = systemPromptCustom
                    .trimmingCharacters(in: .whitespacesAndNewlines)
            }

            let mcpRaw = mcpServersText.trimmingCharacters(in: .whitespacesAndNewlines)
            if !mcpRaw.isEmpty {
                if mcpRaw.hasPrefix("{") {
                    serviceConfig["mcp_servers"] = try parseJSONObjectText(mcpRaw)
                } else {
                    serviceConfig["mcp_servers"] = mcpRaw
                }
            }

            let agentsRaw = agentsText.trimmingCharacters(in: .whitespacesAndNewlines)
            if !agentsRaw.isEmpty {
                serviceConfig["agents"] = try parseJSONObjectText(agentsRaw)
            }

            if restrictTools {
                var tools = Set<String>()
                for t in allowedTools {
                    let s = t.trimmingCharacters(in: .whitespacesAndNewlines)
                    if !s.isEmpty { tools.insert(s) }
                }
                for t in parseCommaNewlineList(extraAllowedToolsText) {
                    tools.insert(t)
                }
                if !tools.isEmpty {
                    serviceConfig["allowed_tools"] = tools.sorted()
                }
            }

            let resp = try await c.createClaudeService(
                imageRef: imageRef,
                rwMounts: mounts,
                env: env,
                serviceConfig: serviceConfig
            )
            serviceID = resp["service_id"] as? String
            if let serviceID {
                statusText = "Created: \(serviceID) (connecting WS...)"
            } else {
                statusText = "Created service"
            }
            chatOutput = ""
            debugThinking = ""
            debugEvents = ""
            await refreshServices()
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
                        case "response.thinking.delta":
                            if (obj["reset"] as? Bool) == true {
                                debugThinking = ""
                            }
                            if let t = obj["text"] as? String {
                                debugThinking += t
                            }
                        case "tool.use", "tool.result", "response.usage":
                            debugEvents += s + "\n"
                        case "agent.ask":
                            debugEvents += s + "\n"
                            guard let askID = obj["ask_id"] as? String,
                                  let input = obj["input"] as? [String: Any],
                                  let rawQuestions = input["questions"] as? [[String: Any]]
                            else {
                                chatOutput += "\n[ASK] invalid payload\n"
                                break
                            }

                            var questions: [AskQuestion] = []
                            for (qi, q) in rawQuestions.enumerated() {
                                let header = (q["header"] as? String) ?? "Question"
                                let qtext = (q["question"] as? String) ?? ""
                                let multi = (q["multiSelect"] as? Bool) ?? false
                                let rawOptions = q["options"] as? [[String: Any]] ?? []

                                var options: [AskOption] = []
                                options.reserveCapacity(rawOptions.count)
                                for (oi, o) in rawOptions.enumerated() {
                                    let label = (o["label"] as? String) ?? ""
                                    let desc = (o["description"] as? String) ?? ""
                                    options.append(AskOption(id: oi, label: label, description: desc))
                                }
                                questions.append(
                                    AskQuestion(
                                        id: qi,
                                        header: header,
                                        question: qtext,
                                        options: options,
                                        multiSelect: multi
                                    )
                                )
                            }
                            pendingAsk = AskRequest(id: askID, questions: questions)
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

    private func sendAskAnswer(askID: String, answers: [String: String]) {
        guard let wsTask else {
            statusText = "WS not connected"
            pendingAsk = nil
            return
        }
        do {
            let payload: [String: Any] = [
                "type": "ask.answer",
                "ask_id": askID,
                "answers": answers,
            ]
            let data = try JSONSerialization.data(withJSONObject: payload)
            guard let json = String(data: data, encoding: .utf8) else {
                throw NousAgentRunnerError.io("failed to encode json")
            }
            wsTask.send(.string(json)) { err in
                if let err {
                    DispatchQueue.main.async { statusText = "WS send error: \(err)" }
                }
            }
            pendingAsk = nil
        } catch {
            statusText = "Ask answer error: \(error)"
            pendingAsk = nil
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
