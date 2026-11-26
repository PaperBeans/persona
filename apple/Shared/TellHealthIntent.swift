#if !WIDGET_EXTENSION
import AppIntents
import Foundation
import PersonaWire
import SwiftUI

#if os(iOS) || os(watchOS)
/// A single sentence goes only to Health; a receipt is acceptance, never a claim that a plan is ready.
struct TellHealthIntent: AppIntent {
    nonisolated static let title: LocalizedStringResource = "Tell Health"
    nonisolated static let description = IntentDescription("Send a sentence to Health, such as “Strength training has started”.")
    nonisolated static var openAppWhenRun: Bool { false }
    @available(iOS 26.0, watchOS 26.0, visionOS 26.0, *)
    nonisolated static var supportedModes: IntentModes { .background }
    @Parameter(title: "Text") var text: String?
    nonisolated init() {}
    nonisolated init(text: String?) { self.text = text }
    nonisolated static var parameterSummary: some ParameterSummary { Summary("Tell Health \(\.$text)") }

    #if (os(iOS) || os(visionOS)) && compiler(>=6.4)
    nonisolated func perform() async throws -> some IntentResult & ReturnsValue<String> & ProvidesDialog & ShowsSnippetView {
        let receipt = try await run()
        return .result(value: receipt.message, dialog: IntentDialog("\(receipt.message)"), view: TellHealthResultView(message: receipt.message))
    }
    #else
    nonisolated func perform() async throws -> some IntentResult & ReturnsValue<String> & ProvidesDialog {
        let receipt = try await run()
        return .result(value: receipt.message, dialog: IntentDialog("\(receipt.message)"))
    }
    #endif

    private nonisolated func run() async throws -> HealthTellReceipt {
        let (api, credential) = try await Task.detached {
            let snapshot = ExtensionSurfaceStore.shared.read()
            let api = try TellHealthFlow.client(snapshot: snapshot, selectedGroup: UserDefaults.standard.string(forKey: "hub.group"))
            guard let credential = snapshot.groups[api.group]?.credential else { throw TellHealthFailure.unavailable }
            return (api, credential)
        }.value
        let features = try await ExtensionSurfaceClient(credential: credential, clientMetadata: .init(surface: .intent)).features()
        return try await TellHealthFlow.send(api, text: text, features: features)
    }
}

#endif

nonisolated struct HealthTellReceipt: Decodable, Sendable {
    var id: String
    var status: String
    var duplicate: Bool?
    var message: String { String(localized: "Sent to Health") }
}

nonisolated enum TellHealthFailure: LocalizedError {
    case unavailable, empty, tooLong, rejected, uncertain(String)
    var errorDescription: String? {
        switch self {
        case .unavailable: String(localized: "This hub doesn't support Tell Health yet. Update the hub first.")
        case .empty: String(localized: "Enter something to tell Health.")
        case .tooLong: String(localized: "This text is too long. Send a shorter message to Health.")
        case .rejected: String(localized: "Couldn't confirm delivery to Health. Open Health to check this message.")
        case .uncertain(let reason): String(localized: "Couldn't confirm sending to Health. Check Health before trying again. \(reason)")
        }
    }
}

nonisolated enum TellHealthFlow {
    static let feature = "health_tell"
    static let path = "/api/v1/assistant/health/tell"
    private struct Request: Encodable { var client_id: String; var text: String }

    static func client(snapshot: ExtensionSurfaceSnapshot, selectedGroup: String?) throws -> APIClient {
        do { return try LogWaterFlow.client(snapshot: snapshot, selectedGroup: selectedGroup) }
        catch { throw LogWaterFailure(message: String(localized: "Open Persona to select and sync a hub before telling Health.")) }
    }

    /// A new invocation has one UUID. Even a lost receipt retries that same request, at most three attempts.
    static func send(_ api: APIClient, text: String?, features: Set<String>,
                     pause: (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) async throws -> HealthTellReceipt {
        guard features.contains(feature) else { throw TellHealthFailure.unavailable }
        let text = (text ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { throw TellHealthFailure.empty }
        guard text.utf8.count <= 8192 else { throw TellHealthFailure.tooLong }
        let body = Request(client_id: UUID().uuidString, text: text)
        for attempt in 0..<3 {
            try Task.checkCancellation()
            do {
                let data = try await api.manager(nil).request("POST", path, body: body)
                let receipt = try JSON.decoder.decode(HealthTellReceipt.self, from: data)
                guard !receipt.id.isEmpty, ["received", "delivered"].contains(receipt.status) else { throw TellHealthFailure.rejected }
                return receipt
            } catch {
                if error is CancellationError || (error as? URLError)?.code == .cancelled { throw error }
                if error is DecodingError { throw TellHealthFailure.uncertain(error.localizedDescription) }
                guard retryable(error) else { throw error }
                guard attempt < 2 else { throw TellHealthFailure.uncertain(error.localizedDescription) }
                try await pause(.milliseconds(250 * (attempt + 1)))
            }
        }
        throw TellHealthFailure.rejected
    }

    private static func retryable(_ error: Error) -> Bool {
        if let error = error as? APIError { return [500, 502, 503, 504].contains(error.status) }
        guard let error = error as? URLError else { return false }
        return [.timedOut, .networkConnectionLost, .notConnectedToInternet, .cannotConnectToHost, .cannotFindHost].contains(error.code)
    }
}

/// The same result view is returned to Shortcuts and used by the explicitly labelled DEBUG preview.
struct TellHealthResultView: View {
    var message: String
    var body: some View {
        Text(message).font(ExtensionSurfaceTokens.body).padding(ExtensionSurfaceTokens.spacing).accessibilityIdentifier("tell-health-result")
    }
}
#endif
