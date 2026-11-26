#if DEBUG && !WIDGET_EXTENSION
import SwiftUI

/// A simulator preview of the actual result view, driven by a real receipt from the synthetic hub.
struct TellHealthPreview: View {
    var api: APIClient?
    @State private var message = String(localized: "Sending to Health…")

    var body: some View {
        ScrollView {
            VStack(spacing: ExtensionSurfaceTokens.spacing) {
                Text("Tell Health · Preview").font(ExtensionSurfaceTokens.label)
                TellHealthResultView(message: message)
            }
        }
        .task {
            guard let api else { message = String(localized: "Open Persona to select and sync a hub before telling Health."); return }
            do {
                struct Capabilities: Decodable { var features: [String]? }
                let capabilities: Capabilities = try await api.get("/api/v1/health")
                let receipt = try await TellHealthFlow.send(api, text: "Strength training has started", features: Set(capabilities.features ?? []))
                message = receipt.message
            } catch { message = error.localizedDescription }
        }
    }
}
#endif
