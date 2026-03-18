import CryptoKit
import Foundation

enum DemoPaths {
    static func instanceID(bundle: Bundle = .main) -> String {
        if let url = bundle.url(forResource: "AgentRunnerConfig", withExtension: "json"),
           let data = try? Data(contentsOf: url),
           let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
           let v = obj["instance_id"] as? String
        {
            let trimmed = v.trimmingCharacters(in: .whitespacesAndNewlines)
            if isSafeInstanceID(trimmed) {
                return trimmed
            }
        }

        if let bundleID = bundle.bundleIdentifier, !bundleID.isEmpty {
            return deriveInstanceIDFromBundleID(bundleID)
        }

        return "default"
    }

    static func appSupportDirURL(bundle: Bundle = .main, fileManager: FileManager = .default) -> URL {
        fileManager.homeDirectoryForCurrentUser
            .appendingPathComponent("Library")
            .appendingPathComponent("Application Support")
            .appendingPathComponent("AgentRunner")
            .appendingPathComponent(instanceID(bundle: bundle))
    }

    static func runnerLogURL(bundle: Bundle = .main, fileManager: FileManager = .default) -> URL {
        appSupportDirURL(bundle: bundle, fileManager: fileManager)
            .appendingPathComponent("runnerd.log")
    }

    static func vmLogsDirURL(bundle: Bundle = .main, fileManager: FileManager = .default) -> URL {
        fileManager.homeDirectoryForCurrentUser
            .appendingPathComponent("Library")
            .appendingPathComponent("Caches")
            .appendingPathComponent("AgentRunner")
            .appendingPathComponent("lima")
            .appendingPathComponent("agent-\(instanceID(bundle: bundle))")
    }

    static func skillsDirURL(bundle: Bundle = .main, fileManager: FileManager = .default) -> URL {
        appSupportDirURL(bundle: bundle, fileManager: fileManager)
            .appendingPathComponent("skills")
    }

    private static func deriveInstanceIDFromBundleID(_ bundleID: String) -> String {
        let normalized = bundleID.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let digest = SHA256.hash(data: Data(normalized.utf8))
        let hex = digest.map { String(format: "%02x", $0) }.joined()
        return String(hex.prefix(12))
    }

    private static func isSafeInstanceID(_ s: String) -> Bool {
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
}
