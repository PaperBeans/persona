#!/usr/bin/env python3
"""Direct AVAudioSession calls live only in the core's AudioSessionBroker (#13849, part of #13431 and #13432).

Every Apple target (iOS, visionOS, watchOS, share extension, feature modules) routes AVAudioSession configuration,
activation, route queries and session notifications through `apple/Shared/AudioSessionBroker.swift` so Voice,
Recording and other modules never compete over the shared session directly.

Allowed outside AudioSessionBroker.swift:
- Pure value types (`AVAudioSession.Port`, `AVAudioSession.Category`, `AVAudioSession.Mode`,
  `AVAudioSession.CategoryOptions`, `AVAudioSession.RouteChangeReason`, `AVAudioSession.InterruptionReason`,
  `AVAudioSessionPortDescription`, `AVAudioSessionRouteDescription`).
- Apple's `ConversationManagerDelegate` protocol signatures (`didActivate audioSession: AVAudioSession` /
  `didDeactivate audioSession: AVAudioSession`), which do not touch `audioSession`.

  python3 scripts/test/test_apple_audio_session_broker.py          run the check (unittest)
  python3 scripts/test/test_apple_audio_session_broker.py --list   print every direct AVAudioSession call, file:line
"""
import os
from pathlib import Path
import re
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "i18n"))
import apple_comments  # noqa: E402
sys.path.insert(0, str(Path(__file__).resolve().parent))
from test_apple_module_refs import blank  # noqa: E402

BROKER = "apple/Shared/AudioSessionBroker.swift"
SKIP_PARTS = {"PersonaTests", "PersonaUITests", "PersonaWatchTests", "Tests", "verify"}
SKIP_DIRS = apple_comments.SKIP_DIRS | {"SourcePackages"}

DIRECT_PATTERNS = (
    ("sharedInstance", re.compile(r"\bAVAudioSession\s*\.\s*sharedInstance\b")),
    ("notification", re.compile(
        r"\bAVAudioSession\s*\.\s*(?:interruption|routeChange|mediaServicesWereReset|mediaServicesWereLost|"
        r"availableInputsChange|silenceSecondaryAudioHint|spatialPlaybackCapabilitiesChanged|"
        r"renderingModeChange|renderingCapabilitiesChange)Notification\b"
    )),
    ("notificationKey", re.compile(r"\bAVAudioSession(?:Interruption|RouteChange)\w*Key\b")),
    ("typedMessage", re.compile(r"\bof\s*:\s*AVAudioSession\s*\.\s*self\b")),
    ("sessionParam", re.compile(r":\s*AVAudioSession(?![.\w])")),
)

# LiveCommunicationKit's ConversationManagerDelegate requires these exact parameter types.
DELEGATE_LINE = re.compile(
    r"\bfunc\s+conversationManager\s*\([^)\n]*(?:didActivate|didDeactivate)\s+audioSession\s*:\s*AVAudioSession\s*\)"
)


def swift_files(root=ROOT):
    apple = root / "apple"
    if not apple.is_dir():
        return
    for directory, dirs, names in os.walk(apple):
        dirs[:] = sorted(d for d in dirs if d not in SKIP_DIRS)
        for name in sorted(names):
            if not name.endswith(".swift"):
                continue
            rel = Path(directory, name).relative_to(root)
            if not SKIP_PARTS.intersection(rel.parts) and not rel.parts[1].endswith("Tests"):
                yield rel


def violations(src):
    """(line, kind, code) for every direct AVAudioSession use in one Swift source string."""
    raw = src.splitlines()
    code = blank(src)
    found = []
    for kind, pattern in DIRECT_PATTERNS:
        for m in pattern.finditer(code):
            line = code.count("\n", 0, m.start()) + 1
            text = raw[line - 1] if 0 < line <= len(raw) else ""
            if kind == "sessionParam" and DELEGATE_LINE.search(code.splitlines()[line - 1]):
                continue
            found.append((line, kind, text.strip()))
    return sorted(set(found), key=lambda item: (item[0], item[1]))


def scan(root=ROOT):
    """`file:line [kind]: code` for every direct AVAudioSession call outside AudioSessionBroker."""
    sites = []
    for rel in swift_files(root):
        if rel.as_posix() == BROKER:
            continue
        src = (root / rel).read_text(encoding="utf-8", errors="replace")
        if "AVAudioSession" not in src:
            continue
        for line, kind, text in violations(src):
            sites.append(f"{rel.as_posix()}:{line} [{kind}]: {text}")
    return sites


class AppleAudioSessionBrokerTests(unittest.TestCase):
    def test_no_direct_avaudiosession_calls_outside_broker(self):
        sites = scan()
        self.assertEqual(
            sites,
            [],
            "direct AVAudioSession call outside apple/Shared/AudioSessionBroker.swift (#13849): "
            "route it through AudioSessionBroker.shared:\n" + "\n".join(sites),
        )

    def test_broker_exists_and_owns_shared_instance(self):
        broker = ROOT / BROKER
        self.assertTrue(broker.is_file(), f"missing {BROKER}")
        code = blank(broker.read_text(encoding="utf-8"))
        self.assertRegex(code, r"\bAVAudioSession\s*\.\s*sharedInstance\s*\(\s*\)")

    def test_what_counts_and_what_does_not(self):
        bad = [
            "let s = AVAudioSession.sharedInstance()",
            'let msg = "\\(AVAudioSession.sharedInstance().category)"',
            "NotificationCenter.default.addObserver(forName: AVAudioSession.routeChangeNotification, object: nil, queue: .main) { _ in }",
            "NotificationCenter.default.addObserver(forName: AVAudioSession.interruptionNotification, object: nil, queue: .main) { _ in }",
            "NotificationCenter.default.addObserver(forName: AVAudioSession.mediaServicesWereResetNotification, object: nil, queue: .main) { _ in }",
            "let k = note.userInfo?[AVAudioSessionInterruptionTypeKey]",
            "let r = note.userInfo?[AVAudioSessionRouteChangeReasonKey]",
            "center.addObserver(of: AVAudioSession.self, for: .didBecomeInactive) { _ in }",
            "func read(_ session: AVAudioSession) -> Bool { true }",
        ]
        for sample in bad:
            with self.subTest(bad=sample):
                self.assertTrue(violations(sample), f"expected violation for: {sample}")

        ok = [
            "// AVAudioSession.sharedInstance()",
            "/* AVAudioSession.routeChangeNotification */",
            'let s = "AVAudioSession.sharedInstance()"',
            "let ports: [AVAudioSession.Port] = [.builtInMic, .bluetoothHFP]",
            "static let options: AVAudioSession.CategoryOptions = [.defaultToSpeaker]",
            "let map: [AVAudioSession.Category: String] = [.playback: \"Play\"]",
            "func handle(_ reason: AVAudioSession.InterruptionReason?) {}",
            "func output(_ route: AVAudioSessionRouteDescription) {}",
            "func device(_ port: AVAudioSessionPortDescription) {}",
            "nonisolated func conversationManager(_ manager: ConversationManager, didActivate audioSession: AVAudioSession) {}",
            "nonisolated func conversationManager(_ manager: ConversationManager, didDeactivate audioSession: AVAudioSession) {}",
        ]
        for sample in ok:
            with self.subTest(ok=sample):
                self.assertEqual(violations(sample), [], f"unexpected violation for: {sample}")

    def test_scan_catches_planted_violation_and_allows_broker(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / BROKER).parent.mkdir(parents=True, exist_ok=True)
            (root / BROKER).write_text("let s = AVAudioSession.sharedInstance()\n", encoding="utf-8")
            bad = root / "apple/Persona/App/Rogue.swift"
            bad.parent.mkdir(parents=True, exist_ok=True)
            bad.write_text("func f() { _ = AVAudioSession.sharedInstance().isOtherAudioPlaying }\n", encoding="utf-8")
            test_file = root / "apple/PersonaTests/AudioTests.swift"
            test_file.parent.mkdir(parents=True, exist_ok=True)
            test_file.write_text("let s = AVAudioSession.sharedInstance()\n", encoding="utf-8")
            self.assertEqual(
                scan(root),
                ["apple/Persona/App/Rogue.swift:1 [sharedInstance]: func f() { _ = AVAudioSession.sharedInstance().isOtherAudioPlaying }"],
            )


if __name__ == "__main__":
    if sys.argv[1:] == ["--list"]:
        print("\n".join(scan()))
        sys.exit(0)
    unittest.main()
