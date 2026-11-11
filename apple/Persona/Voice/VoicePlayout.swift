import Foundation

/// The voice's jitter buffer (#4430). The extension relays the upstream's audio as it comes, and a piece that reaches the
/// player after the one before it finished leaves a gap: scheduled the moment it arrived, the voice played a couple of
/// syllables, stopped, and went on. So when nothing is playing, the pieces are held until `lead` of them has gathered,
/// or the first has waited `wait` (a short reply, the tail of one), and go to the player together; while it plays, each
/// goes straight behind the last. A player that runs dry mid-reply gathers again: one short pause instead of many.
/// A service that sends faster than it plays fills the lead almost at once.
nonisolated struct VoicePlayout<Piece> {
    /// How much to gather before playing.
    static var lead: TimeInterval { 0.2 }
    /// The longest the first held piece waits for the rest of the lead.
    static var wait: TimeInterval { 0.3 }

    /// The lead in frames at the voice's rate.
    let leadFrames: Int
    private(set) var held: [Piece] = []
    private var heldFrames = 0

    init(rate: Int) {
        leadFrames = max(1, Int(Double(rate) * Self.lead))
    }

    /// Something is held, waiting for the lead or for `wait` to run out.
    var isHolding: Bool { !held.isEmpty }

    /// A piece of the voice, `frames` long, arrived while the player had some of it queued (`playing`) or none: what
    /// to schedule now, in order (empty while it is held).
    mutating func add(_ piece: Piece, frames: Int, playing: Bool) -> [Piece] {
        if playing, held.isEmpty { return [piece] }
        held.append(piece)
        heldFrames += frames
        return heldFrames >= leadFrames ? release() : []
    }

    /// What is held, to play now (the wait ran out); nothing is held afterwards.
    mutating func release() -> [Piece] {
        defer {
            held = []
            heldFrames = 0
        }
        return held
    }

    /// Drops what is held (`clear`, a new player).
    mutating func clear() {
        held = []
        heldFrames = 0
    }
}
