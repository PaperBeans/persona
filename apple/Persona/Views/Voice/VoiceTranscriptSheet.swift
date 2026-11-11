import SwiftUI

/// ⋯ › All Captions (docs/assistant/voice.md 6.2): everything the call heard and said so far (its last 40 lines), oldest first. Only
/// in this call: nothing is saved, and the conversation keeps the instructions and the answers as ever.
struct VoiceTranscriptSheet: View {
    let call: VoiceConversation
    let persona: String
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: Theme.Space.m) {
                    Text("Captions last only for this call and aren’t saved. Instructions and answers stay in the chat as usual.")
                        .font(Theme.Font.caption)
                        .foregroundStyle(.secondary)
                    ForEach(call.captions) { caption in
                        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                            Text(caption.isUser ? String(localized: "You") : persona)
                                .font(Theme.Font.labelStrong)
                                .foregroundStyle(caption.isUser ? Color.secondary : Theme.persona)
                            Text(caption.text)
                                .font(Theme.Font.body)
                                .foregroundStyle(caption.final ? Color.primary : Color.secondary)
                                .textSelection(.enabled)
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .accessibilityElement(children: .combine)
                    }
                    if call.captions.isEmpty {
                        EmptyStateView(symbol: Theme.Icon.voiceCaptions, title: String(localized: "No Captions Yet"),
                                       message: String(localized: "Say something and captions will appear here."))
                    }
                }
                .padding(Theme.Space.page)
            }
            .defaultScrollAnchor(.bottom)
            .themedPage()
            .navigationTitle("Captions")
            #if os(iOS) || os(visionOS)
            .navigationBarTitleDisplayMode(.inline)
            #endif
            #if os(macOS)
            .macSheetDone { dismiss() }
            #else
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done [Extension System Presentation c0b3fb]") { dismiss() }.lineLimit(1)
                }
            }
            #endif
        }
        .accessibilityIdentifier("voice-transcript")
    }
}
