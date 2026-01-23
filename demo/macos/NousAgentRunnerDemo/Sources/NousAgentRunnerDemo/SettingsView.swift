import SwiftUI

struct SettingsView: View {
    @Binding var serviceEnvText: String
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Settings")
                .font(.title3)

            GroupBox("Service env (per container)") {
                VStack(alignment: .leading, spacing: 8) {
                    Text("One per line: KEY=VALUE. Lines starting with # are ignored. Keys starting with NOUS_ are reserved.")
                        .font(.caption)
                        .foregroundStyle(.secondary)

                    TextEditor(text: $serviceEnvText)
                        .font(.system(.body, design: .monospaced))
                        .frame(minHeight: 220)
                        .overlay {
                            RoundedRectangle(cornerRadius: 6)
                                .stroke(.quaternary, lineWidth: 1)
                        }
                }
            }

            HStack {
                Spacer()
                Button("Close") { dismiss() }
            }
        }
        .padding(16)
        .frame(minWidth: 640, minHeight: 360)
    }
}

