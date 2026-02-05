import Foundation
import CryptoKit

public enum NousAgentRunnerError: Error {
    case missingConfig(String)
    case invalidConfig(String)
    case io(String)
    case http(Int, String)
    case timeout(String)
}

public struct NousAgentRunnerRuntime: Sendable {
    public let baseURL: URL
    public let token: String
    public let instanceID: String

    public init(baseURL: URL, token: String, instanceID: String) {
        self.baseURL = baseURL
        self.token = token
        self.instanceID = instanceID
    }

    public static func discover() throws -> NousAgentRunnerRuntime {
        let instanceID = try loadInstanceIDFromBundle()
        return try discover(instanceID: instanceID)
    }

    public static func discover(instanceID: String) throws -> NousAgentRunnerRuntime {
        let appSupportDir = try resolveAppSupportDir(instanceID: instanceID)
        let port = try loadPort(appSupportDir: appSupportDir)
        let token = try loadToken(appSupportDir: appSupportDir)
        guard let baseURL = URL(string: "http://127.0.0.1:\(port)") else {
            throw NousAgentRunnerError.invalidConfig("invalid base url")
        }
        return NousAgentRunnerRuntime(baseURL: baseURL, token: token, instanceID: instanceID)
    }
}

public final class NousAgentRunnerDaemon {
    private let instanceID: String
    private var process: Process?
    private var logFileHandle: FileHandle?

    public init(instanceID: String? = nil) throws {
        self.instanceID = try instanceID ?? loadInstanceIDFromBundle()
    }

    public func ensureRunning(timeoutSeconds: Double = 15) async throws -> NousAgentRunnerRuntime {
        if let runtime = try? NousAgentRunnerRuntime.discover(instanceID: instanceID) {
            let client = NousAgentRunnerClient(runtime: runtime)
            if (try? await client.getSystemStatus()) != nil {
                return runtime
            }
        }

        if process == nil {
            let runnerURL = try locateBundledExecutable(named: "nous-agent-runnerd")
            let p = Process()
            p.executableURL = runnerURL

            // Persist logs to App Support for debugging (Pipe is not read and may stall).
            let appSupportDir = try resolveAppSupportDir(instanceID: instanceID)
            try FileManager.default.createDirectory(at: appSupportDir, withIntermediateDirectories: true)
            let logURL = appSupportDir.appendingPathComponent("runnerd.log")
            if !FileManager.default.fileExists(atPath: logURL.path) {
                FileManager.default.createFile(atPath: logURL.path, contents: nil)
            }
            let fh = try FileHandle(forWritingTo: logURL)
            try fh.seekToEnd()
            let header = "\n--- runnerd start \(Date()) ---\n"
            if let data = header.data(using: .utf8) {
                try? fh.write(contentsOf: data)
            }
            p.standardOutput = fh
            p.standardError = fh
            logFileHandle = fh

            try p.run()
            process = p
        }

        let deadline = Date().addingTimeInterval(timeoutSeconds)
        while Date() < deadline {
            if let runtime = try? NousAgentRunnerRuntime.discover(instanceID: instanceID) {
                let client = NousAgentRunnerClient(runtime: runtime)
                if (try? await client.getSystemStatus()) != nil {
                    return runtime
                }
            }
            try await Task.sleep(nanoseconds: 200_000_000)
        }
        throw NousAgentRunnerError.timeout("timeout waiting for nous-agent-runnerd")
    }

    public func stop() {
        guard let p = process else { return }
        p.terminate()
        process = nil
        try? logFileHandle?.close()
        logFileHandle = nil
    }
}

public final class NousAgentRunnerClient {
    private let runtime: NousAgentRunnerRuntime
    private let session: URLSession

    public init(runtime: NousAgentRunnerRuntime, session: URLSession = .shared) {
        self.runtime = runtime
        self.session = session
    }

    public func getSystemStatus() async throws -> [String: Any] {
        try await requestJSON(method: "GET", path: "/v1/system/status", body: nil, timeoutSeconds: 30)
    }

    public func diagnoseGuestToHostTunnel() async throws -> [String: Any] {
        try await requestJSON(method: "POST", path: "/v1/system/diagnostics/guest_to_host_tunnel", body: nil, timeoutSeconds: 600)
    }

    public func getSystemPaths() async throws -> [String: Any] {
        try await requestJSON(method: "GET", path: "/v1/system/paths", body: nil, timeoutSeconds: 30)
    }

    public func listShares() async throws -> [String: Any] {
        try await requestJSON(method: "GET", path: "/v1/shares", body: nil, timeoutSeconds: 30)
    }

    public func addShare(hostPath: String) async throws -> [String: Any] {
        try await requestJSON(method: "POST", path: "/v1/shares", body: ["host_path": hostPath], timeoutSeconds: 60)
    }

    public func listSkills() async throws -> [String: Any] {
        try await requestJSON(method: "GET", path: "/v1/skills", body: nil, timeoutSeconds: 30)
    }

    public func discoverSkills(source: String, ref: String? = nil, subpath: String? = nil) async throws -> [String: Any] {
        let trimmed = source.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty {
            throw NousAgentRunnerError.invalidConfig("source is required")
        }
        var body: [String: Any] = ["source": trimmed]
        if let ref, !ref.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            body["ref"] = ref
        }
        if let subpath, !subpath.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            body["subpath"] = subpath
        }
        return try await requestJSON(method: "POST", path: "/v1/skills/discover", body: body, timeoutSeconds: 1800)
    }

    public func installSkills(source: String, ref: String? = nil, subpath: String? = nil, skills: [String] = [], replace: Bool = false) async throws -> [String: Any] {
        let trimmed = source.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty {
            throw NousAgentRunnerError.invalidConfig("source is required")
        }
        var body: [String: Any] = ["source": trimmed]
        if let ref, !ref.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            body["ref"] = ref
        }
        if let subpath, !subpath.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            body["subpath"] = subpath
        }
        if !skills.isEmpty {
            body["skills"] = skills
        }
        if replace {
            body["replace"] = true
        }
        return try await requestJSON(method: "POST", path: "/v1/skills/install", body: body, timeoutSeconds: 1800)
    }

    public func deleteSkill(name: String) async throws -> [String: Any] {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty || !isSafeSkillDirName(trimmed) {
            throw NousAgentRunnerError.invalidConfig("invalid skill name")
        }
        return try await requestJSON(method: "DELETE", path: "/v1/skills/\(trimmed)", body: nil, timeoutSeconds: 60)
    }

    public func pullImage(ref: String) async throws -> [String: Any] {
        try await requestJSON(method: "POST", path: "/v1/images/pull", body: ["ref": ref], timeoutSeconds: 1800)
    }

    public func pruneImages(all: Bool? = nil) async throws -> [String: Any] {
        let body: [String: Any]?
        if let all {
            body = ["all": all]
        } else {
            body = nil
        }
        return try await requestJSON(method: "POST", path: "/v1/images/prune", body: body, timeoutSeconds: 1800)
    }

    public func restartVM() async throws -> [String: Any] {
        try await requestJSON(method: "POST", path: "/v1/system/vm/restart", body: nil, timeoutSeconds: 1800)
    }

    public func createClaudeService(imageRef: String, rwMounts: [String], env: [String: String] = [:], idleTimeoutSeconds: Int = 0, serviceConfig: [String: Any]) async throws -> [String: Any] {
        if idleTimeoutSeconds < 0 {
            throw NousAgentRunnerError.invalidConfig("idle_timeout_seconds must be >= 0")
        }
        let body: [String: Any] = [
            "type": "claude",
            "image_ref": imageRef,
            "resources": [
                "cpu_cores": 2,
                "memory_mb": 1024,
                "pids": 256,
            ],
            "rw_mounts": rwMounts,
            "env": env,
            "idle_timeout_seconds": idleTimeoutSeconds,
            "service_config": serviceConfig,
        ]
        return try await requestJSON(method: "POST", path: "/v1/services", body: body, timeoutSeconds: 1800)
    }

    public func listServices() async throws -> [String: Any] {
        try await requestJSON(method: "GET", path: "/v1/services", body: nil, timeoutSeconds: 30)
    }

    public func getService(serviceID: String) async throws -> [String: Any] {
        try await requestJSON(method: "GET", path: "/v1/services/\(serviceID)", body: nil, timeoutSeconds: 30)
    }

    public func getBuiltinTools(serviceType: String) async throws -> [String: Any] {
        try await requestJSON(method: "GET", path: "/v1/services/types/\(serviceType)/builtin_tools", body: nil, timeoutSeconds: 30)
    }

    public func deleteService(serviceID: String) async throws -> [String: Any] {
        try await requestJSON(method: "DELETE", path: "/v1/services/\(serviceID)", body: nil, timeoutSeconds: 300)
    }

    public func stopService(serviceID: String) async throws -> [String: Any] {
        try await requestJSON(method: "POST", path: "/v1/services/\(serviceID)/stop", body: nil, timeoutSeconds: 300)
    }

    public func startService(serviceID: String) async throws -> [String: Any] {
        try await requestJSON(method: "POST", path: "/v1/services/\(serviceID)/start", body: nil, timeoutSeconds: 300)
    }

    public func resumeService(serviceID: String) async throws -> [String: Any] {
        try await requestJSON(method: "POST", path: "/v1/services/\(serviceID)/resume", body: nil, timeoutSeconds: 1800)
    }

    public func createTunnel(hostPort: Int, guestPort: Int? = nil) async throws -> [String: Any] {
        var body: [String: Any] = ["host_port": hostPort]
        if let guestPort {
            body["guest_port"] = guestPort
        }
        return try await requestJSON(method: "POST", path: "/v1/tunnels", body: body, timeoutSeconds: 60)
    }

    public func listTunnels() async throws -> [String: Any] {
        try await requestJSON(method: "GET", path: "/v1/tunnels", body: nil, timeoutSeconds: 30)
    }

    public func getTunnelByHostPort(hostPort: Int) async throws -> [String: Any] {
        try await requestJSON(method: "GET", path: "/v1/tunnels/by_host_port/\(hostPort)", body: nil, timeoutSeconds: 30)
    }

    public func deleteTunnel(tunnelID: String) async throws -> [String: Any] {
        try await requestJSON(method: "DELETE", path: "/v1/tunnels/\(tunnelID)", body: nil, timeoutSeconds: 60)
    }

    public func deleteTunnelByHostPort(hostPort: Int) async throws -> [String: Any] {
        try await requestJSON(method: "DELETE", path: "/v1/tunnels/by_host_port/\(hostPort)", body: nil, timeoutSeconds: 60)
    }

    public func openChatWebSocket(serviceID: String) throws -> URLSessionWebSocketTask {
        guard var components = URLComponents(url: runtime.baseURL, resolvingAgainstBaseURL: false) else {
            throw NousAgentRunnerError.invalidConfig("invalid base url")
        }
        components.scheme = "ws"
        components.path = "/v1/services/\(serviceID)/chat"
        guard let url = components.url else {
            throw NousAgentRunnerError.invalidConfig("invalid websocket url")
        }

        var req = URLRequest(url: url)
        req.setValue("Bearer \(runtime.token)", forHTTPHeaderField: "Authorization")
        return session.webSocketTask(with: req)
    }

    private func requestJSON(method: String, path: String, body: [String: Any]?, timeoutSeconds: TimeInterval) async throws -> [String: Any] {
        let url = runtime.baseURL.appendingPathComponent(path)
        var req = URLRequest(url: url)
        req.httpMethod = method
        req.timeoutInterval = timeoutSeconds
        req.setValue("Bearer \(runtime.token)", forHTTPHeaderField: "Authorization")
        if let body {
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            req.httpBody = try JSONSerialization.data(withJSONObject: body)
        }

        let (data, resp) = try await session.data(for: req)
        guard let httpResp = resp as? HTTPURLResponse else {
            throw NousAgentRunnerError.io("no http response")
        }
        guard httpResp.statusCode == 200 else {
            let msg = String(data: data, encoding: .utf8) ?? ""
            throw NousAgentRunnerError.http(httpResp.statusCode, msg)
        }
        let obj = try JSONSerialization.jsonObject(with: data)
        return obj as? [String: Any] ?? [:]
    }
}

private func loadInstanceIDFromBundle() throws -> String {
    if let url = Bundle.main.url(forResource: "NousAgentRunnerConfig", withExtension: "json") {
        let data = try Data(contentsOf: url)
        let obj = try JSONSerialization.jsonObject(with: data)
        guard let dict = obj as? [String: Any] else {
            throw NousAgentRunnerError.invalidConfig("NousAgentRunnerConfig.json must be an object")
        }
        if let instanceID = dict["instance_id"] as? String {
            let trimmed = instanceID.trimmingCharacters(in: .whitespacesAndNewlines)
            if isSafeInstanceID(trimmed) {
                return trimmed
            }
        }
    }

    if let bundleID = Bundle.main.bundleIdentifier, !bundleID.isEmpty {
        return deriveInstanceIDFromBundleID(bundleID)
    }

    return "default"
}

private func deriveInstanceIDFromBundleID(_ bundleID: String) -> String {
    let normalized = bundleID.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    let digest = SHA256.hash(data: Data(normalized.utf8))
    let hex = digest.map { String(format: "%02x", $0) }.joined()
    return String(hex.prefix(12))
}

private func isSafeInstanceID(_ s: String) -> Bool {
    if s.isEmpty { return false }
    for scalar in s.unicodeScalars {
        switch scalar.value {
        case 0x30...0x39: // 0-9
            continue
        case 0x41...0x5A: // A-Z
            continue
        case 0x61...0x7A: // a-z
            continue
        case 0x2D, 0x2E, 0x5F: // - . _
            continue
        default:
            return false
        }
    }
    return true
}

private func isSafeSkillDirName(_ s: String) -> Bool {
    if s.isEmpty { return false }
    for scalar in s.unicodeScalars {
        switch scalar.value {
        case 0x30...0x39: // 0-9
            continue
        case 0x41...0x5A: // A-Z
            continue
        case 0x61...0x7A: // a-z
            continue
        case 0x2D, 0x2E, 0x5F: // - . _
            continue
        default:
            return false
        }
    }
    return true
}

private func resolveAppSupportDir(instanceID: String) throws -> URL {
    guard let home = FileManager.default.homeDirectoryForCurrentUser as URL? else {
        throw NousAgentRunnerError.io("missing home directory")
    }
    return home
        .appendingPathComponent("Library")
        .appendingPathComponent("Application Support")
        .appendingPathComponent("NousAgentRunner")
        .appendingPathComponent(instanceID)
}

private func loadPort(appSupportDir: URL) throws -> Int {
    if let port = loadPortFromRuntimeJSON(appSupportDir: appSupportDir) {
        return port
    }
    let candidates = [".env.local", ".env.production", ".env.development", ".env.test"]
    for name in candidates {
        let url = appSupportDir.appendingPathComponent(name)
        if let contents = try? String(contentsOf: url), let port = parseEnv(contents)["NOUS_AGENT_RUNNER_PORT"], let n = Int(port) {
            return n
        }
    }
    throw NousAgentRunnerError.missingConfig("NOUS_AGENT_RUNNER_PORT not found")
}

private func loadPortFromRuntimeJSON(appSupportDir: URL) -> Int? {
    let url = appSupportDir.appendingPathComponent("runtime.json")
    guard let data = try? Data(contentsOf: url),
          let obj = try? JSONSerialization.jsonObject(with: data),
          let dict = obj as? [String: Any]
    else { return nil }
    return dict["listen_port"] as? Int
}

private func loadToken(appSupportDir: URL) throws -> String {
    let url = appSupportDir.appendingPathComponent("token")
    let token = (try? String(contentsOf: url))?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    if token.isEmpty {
        throw NousAgentRunnerError.missingConfig("token file not found")
    }
    return token
}

private func locateBundledExecutable(named name: String) throws -> URL {
    if let url = Bundle.main.url(forResource: name, withExtension: nil) {
        return url
    }
    if let resourceURL = Bundle.main.resourceURL {
        let url = resourceURL.appendingPathComponent(name)
        if FileManager.default.isExecutableFile(atPath: url.path) {
            return url
        }
    }
    let bundleURL = Bundle.main.bundleURL
    let candidates = [
        bundleURL.appendingPathComponent("Contents/Resources").appendingPathComponent(name),
        bundleURL.appendingPathComponent("Contents/MacOS").appendingPathComponent(name),
    ]
    for url in candidates {
        if FileManager.default.isExecutableFile(atPath: url.path) {
            return url
        }
    }
    throw NousAgentRunnerError.missingConfig("missing bundled executable: \(name)")
}

private func parseEnv(_ content: String) -> [String: String] {
    var out: [String: String] = [:]
    for line in content.split(separator: "\n") {
        let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty || trimmed.hasPrefix("#") { continue }
        let parts = trimmed.split(separator: "=", maxSplits: 1)
        if parts.count != 2 { continue }
        let key = String(parts[0]).trimmingCharacters(in: .whitespacesAndNewlines)
        var value = String(parts[1]).trimmingCharacters(in: .whitespacesAndNewlines)
        value = value.trimmingCharacters(in: CharacterSet(charactersIn: "\"'"))
        out[key] = value
    }
    return out
}
