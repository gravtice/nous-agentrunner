import SwiftUI
import NousAgentRunnerKit

struct ContentView: View {
    @State private var statusText = "Not loaded"
    @State private var imageRef = "registry.nous.ai/claude-agent-service:0.1.0"
    @State private var systemPrompt = "You are a helpful agent."
    @State private var rwMount = ""
    @State private var serviceID: String?
    @State private var chatInput = ""
    @State private var chatOutput = ""

    @State private var wsTask: URLSessionWebSocketTask?
    @State private var daemon: NousAgentRunnerDaemon?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Nous Agent Runner Demo")
                .font(.title2)

            Text(statusText)
                .font(.system(.body, design: .monospaced))
                .frame(maxWidth: .infinity, alignment: .leading)

            HStack {
                Button("Refresh Status") { Task { await refreshStatus() } }
                Button("Create Claude Service") { Task { await createService() } }
                if let serviceID {
                    Button("Connect WS") { connectWS(serviceID: serviceID) }
                }
            }

            GroupBox("Service") {
                VStack(alignment: .leading) {
                    TextField("image_ref", text: $imageRef)
                    TextField("rw_mount (optional)", text: $rwMount)
                    TextField("system_prompt", text: $systemPrompt)
                    if let serviceID {
                        Text("service_id: \(serviceID)")
                            .font(.system(.body, design: .monospaced))
                    }
                }
            }

            GroupBox("Chat") {
                VStack(alignment: .leading) {
                    TextEditor(text: $chatOutput)
                        .frame(minHeight: 200)
                        .font(.system(.body, design: .monospaced))

                    HStack {
                        TextField("message", text: $chatInput)
                        Button("Send") { sendChat() }
                    }
                }
            }

            Spacer()
        }
        .padding(16)
        .frame(minWidth: 700, minHeight: 600)
        .task {
            await ensureRunnerRunning()
            await refreshStatus()
        }
    }

    private func client() throws -> NousAgentRunnerClient {
        let runtime = try NousAgentRunnerRuntime.discover()
        return NousAgentRunnerClient(runtime: runtime)
    }

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

    private func refreshStatus() async {
        do {
            let c = try client()
            let status = try await c.getSystemStatus()
            statusText = "\(status)"
        } catch {
            statusText = "Error: \(error)"
        }
    }

    private func createService() async {
        do {
            let c = try client()
            let rw = rwMount.trimmingCharacters(in: .whitespacesAndNewlines)
            let mounts = rw.isEmpty ? [] : [rw]
            let resp = try await c.createClaudeService(imageRef: imageRef, rwMounts: mounts, serviceConfig: [
                "system_prompt": systemPrompt,
            ])
            serviceID = resp["service_id"] as? String
            chatOutput = ""
        } catch {
            statusText = "Error: \(error)"
        }
    }

    private func connectWS(serviceID: String) {
        do {
            let c = try client()
            let ws = try c.openChatWebSocket(serviceID: serviceID)
            wsTask = ws
            ws.resume()
            receiveLoop()
        } catch {
            statusText = "Error: \(error)"
        }
    }

    private func receiveLoop() {
        guard let wsTask else { return }
        wsTask.receive { result in
            switch result {
            case .failure(let err):
                DispatchQueue.main.async { statusText = "WS error: \(err)" }
            case .success(let msg):
                switch msg {
                case .string(let s):
                    DispatchQueue.main.async { chatOutput += s + "\n" }
                default:
                    break
                }
                receiveLoop()
            }
        }
    }

    private func sendChat() {
        guard let wsTask else { return }
        let text = chatInput.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        chatInput = ""
        let payload: [String: Any] = [
            "type": "input",
            "contents": [
                ["kind": "text", "text": text],
            ],
        ]
        guard let data = try? JSONSerialization.data(withJSONObject: payload),
              let json = String(data: data, encoding: .utf8)
        else { return }
        wsTask.send(.string(json)) { err in
            if let err {
                DispatchQueue.main.async { statusText = "WS send error: \(err)" }
            }
        }
    }
}
