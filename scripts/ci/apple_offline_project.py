#!/usr/bin/env python3
"""Materialize an offline-only graph from a fully resolved XcodeGen JSON spec.

The ordinary project remains unchanged. The full offline project keeps the real
Persona host and every PersonaTests source/selector, but excludes UI-test and
unreferenced Watch/Share/Vision/About targets instead of compiling them again.
Only explicitly focused lanes narrow test sources to their executed suites and
reviewed shared helpers; command-line -only-testing flags alone cannot do that.
"""

import argparse
from copy import deepcopy
import json
from pathlib import Path
import re


SCHEME = "PersonaOfflineUnitTests"
EXCLUDED = {
    "PersonaWatch", "PersonaWatchWidgets", "PersonaShare",
    "PersonaVision", "PersonaVisionWidgets", "PersonaVisionNotify", "PersonaVisionShare",
    "PersonaUITests", "AboutVersionAccessibilityHost", "AboutVersionAccessibilityUITests",
}
CANONICAL_SCHEMES = {
    "Persona", SCHEME, "AboutVersionVisualTests", "AboutVersionAccessibilityHost", "PersonaWatch", "PersonaVision",
}
PACKAGES = {
    "MarkdownUI": {"url": "https://github.com/gonzalezreal/swift-markdown-ui", "from": "2.4.1"},
    "BeautifulUI": {"path": "Packages/BeautifulUI"},
    "PersonaSSH": {"path": "Packages/PersonaSSH"},
    "PersonaKit": {"path": "Packages/PersonaKit"},
    "PersonaWire": {"path": "Packages/PersonaWire"},
    "PersonaCore": {"path": "Packages/PersonaCore"},
    "PersonaScreenKit": {"path": "Packages/PersonaScreenKit"},
    "PersonaInputKit": {"path": "Packages/PersonaInputKit"},
    "PersonaModuleAPI": {"path": "Packages/PersonaModuleAPI"},
    "MarqueeGlow": {"url": "https://github.com/szupzj18/MarqueeGlow", "exactVersion": "0.1.0"},
}
MODULE_CONDITIONS = "$(inherited) PERSONA_MODULE_SCREEN_SHARE PERSONA_MODULE_HEALTH PERSONA_MODULE_VOICE"
SOURCES = {
    "Persona": [
        {"path": "Modules/ScreenShare", "excludes": ["**/*.md"]},
        {"path": "Modules/Health", "excludes": ["**/*.md"]},
        {"path": "Modules/Voice", "excludes": ["**/*.md"]},
        {"path": "Persona", "excludes": ["**/*.md"]},
        {"path": "Shared"},
        {"path": "LiveActivity"},
    ],
    "PersonaTests": [
        {"path": "PersonaTests", "excludes": ["*.json", "*.py"]},
        {"path": "../android/refract/about/AboutVersionRow.swift"},
    ],
    "PersonaWidgets": [
        *({"path": path} for path in (
            "PersonaWidgets", "LiveActivity", "Shared/PersonaSelection.swift", "Shared/FleetState.swift",
            "Shared/StateNames.swift", "Shared/NativeStateMigration.swift", "Shared/StatusPalette.swift",
            "Shared/Design/Palette.swift", "Shared/Design/IslandMetrics.swift", "Shared/HubNaming.swift",
            "Shared/ExtensionUIContext.swift", "Shared/ExtensionSystemAPI.swift", "Shared/ExtensionSystemLink.swift",
            "Shared/ExtensionSurfaceStore.swift", "Shared/ExtensionSurfaceClient.swift",
            "Shared/RequestClientMetadata.swift", "Shared/ExtensionSurfaceEntities.swift",
            "Shared/ExtensionSurfaceView.swift", "Shared/ExtensionSystemWidget.swift",
            "Shared/Design/ExtensionSurfaceTokens.swift", "Shared/Design/PersonaFace.swift", "Shared/HomeWidgets",
            "Shared/Design/HomeWidgetTokens.swift", "Shared/Design/HealthIntakePalette.swift",
            "Shared/Localization",
        )),
    ],
    "PersonaNotify": [
        {"path": "PersonaNotify", "excludes": ["*.plist", "*.entitlements"]},
        {"path": "Shared/PushCrypto.swift"},
        {"path": "Shared/NativeStateMigration.swift"},
        {"path": "Shared/ExtensionNotificationActions.swift"},
    ],
}
DEPENDENCIES = {
    "Persona": [
        {"target": "PersonaWatch"},
        {"target": "PersonaShare"},
        {"sdk": "LiveCommunicationKit.framework", "weak": True},
        {"package": "PersonaWire"},
        {"target": "PersonaWidgets"},
        {"target": "PersonaNotify"},
        *({"package": name} for name in (
            "MarkdownUI", "BeautifulUI", "PersonaSSH", "PersonaKit", "PersonaCore",
            "MarqueeGlow", "PersonaScreenKit", "PersonaInputKit", "PersonaModuleAPI",
        )),
    ],
    "PersonaTests": [
        {"package": "PersonaWire"},
        {"package": "PersonaModuleAPI"},
        {"target": "Persona"},
        {"sdk": "LiveCommunicationKit.framework", "weak": True},
        {"package": "PersonaCore", "product": "PersonaCoreTestSupport"},
    ],
    "PersonaWidgets": [{"package": "PersonaWire"}],
    "PersonaNotify": [{"package": "PersonaWire"}],
}
OFFLINE_ACTION = {
    "build": {"targets": {"Persona": "all", "PersonaTests": ["test"]}},
    "run": {"config": "Debug"},
    "test": {"targets": ["PersonaTests"], "language": "zh-Hans", "region": "CN"},
}
# Execution selectors do not change Swift target membership. Keep every suite
# these focused lanes execute, including both readiness probes and the regular
# iPad fixture, while leaving the ordinary full-test target byte-for-byte alone.
FOCUSED_FILES = (
    "AboutVersionRefractSupport.swift",  # AboutVersionVisualHost's production/shared models.
    "AboutVersionRefractTests.swift",   # Compact and regular visual suites share this file.
    "FakeHub.swift",                    # StarredMessagesTests constructs its real API client here.
    "LauncherModelChoiceTests.swift",
    "OmpNewAgentTests.swift",
    "PageSearchableTests.swift",
    "PaseoPermissionModesTests.swift",
    "PaseoProvidersTests.swift",
    "StarredMessagesTests.swift",
    "TypeScaleTests.swift",             # Both iPhone and iPad readiness probes.
)
FOCUSED_SUITES = {
    "AboutVersionRefractTests.swift": (
        "AboutVersionCompactVisualTests", "AboutVersionRegularVisualTests",
    ),
    **{name + "Tests.swift": (name + "Tests",) for name in (
        "LauncherModelChoice", "OmpNewAgent", "PageSearchable",
        "PaseoPermissionModes", "PaseoProviders", "StarredMessages", "TypeScale",
    )},
}
IPAD_DIAGNOSTIC_SUITES = {
    "AssistantHTMLCardTests.swift": ("AssistantHTMLCardTests",),
    "AssistantHTMLSecurityTests.swift": ("AssistantHTMLSecurityTests",),
}


def focused_sources(project, source_root=None, html_batches=False):
    """Resolve explicit test files and their reviewed cross-file dependencies."""
    root = Path(source_root) if source_root is not None else Path(__file__).resolve().parents[2] / "apple" / "PersonaTests"
    declarations = dict(FOCUSED_SUITES)
    if html_batches:
        # ios-ipad-diagnostic executes these existing separate batches before its
        # regular-width probe; the focused ios lane deliberately does not.
        declarations.update(IPAD_DIAGNOSTIC_SUITES)
    files = list(FOCUSED_FILES)
    if html_batches:
        files.extend(IPAD_DIAGNOSTIC_SUITES)
    for name in files:
        path = root / name
        if not path.is_file():
            raise ValueError(f"focused test source {name} is missing; review its helpers and selectors")
    for name, suites in declarations.items():
        source = (root / name).read_text()
        for suite in suites:
            declaration = re.search(rf"\b(?:class|struct|enum|actor)\s+{re.escape(suite)}\s*\{{", source)
            # Do not approximate Swift parsing here. Exact declarations and
            # source existence guard graph drift; strict xcresult verification
            # remains responsible for executed-suite and zero-test failures.
            if not declaration:
                raise ValueError(f"focused suite {suite} is missing from {name}; review the source list")
    canonical = project["targets"]["PersonaTests"]["sources"]
    source_path = canonical[0]["path"]
    return [
        {"path": source_path, "includes": files},
        deepcopy(canonical[1]),
    ]


def _offline_action(action):
    action = deepcopy(action)
    # A top-level include merges identical test-action arrays, so XcodeGen's
    # resolved JSON repeats this one test entry. Deduplicate only that exact
    # scheme bookkeeping; command-line suite selectors remain untouched.
    if action["build"]["targets"].get("PersonaTests") == ["test", "test"]:
        action["build"]["targets"]["PersonaTests"] = ["test"]
    if action["test"]["targets"] == ["PersonaTests", "PersonaTests"]:
        action["test"]["targets"] = ["PersonaTests"]
    return action


def validate_canonical_graph(project):
    """Refuse to narrow a changed host or test graph into a false green."""
    for name in SOURCES:
        target = project["targets"][name]
        if target["sources"] != SOURCES[name]:
            raise ValueError(f"the canonical {name} sources changed; review the offline graph")
        if target["dependencies"] != DEPENDENCIES[name]:
            if name == "PersonaTests" and not any(dependency == {"target": "Persona"}
                                                   for dependency in target["dependencies"]):
                raise ValueError("offline tests must retain the real Persona host dependency")
            raise ValueError(f"the canonical {name} dependencies changed; review the offline graph")
    conditions = project["targets"]["Persona"]["settings"]["base"].get("SWIFT_ACTIVE_COMPILATION_CONDITIONS")
    if conditions != MODULE_CONDITIONS:
        raise ValueError("the canonical Persona module compilation conditions changed; review the offline graph")
    for name, kind, platform in (
        ("Persona", "application", "iOS"),
        ("PersonaTests", "bundle.unit-test", "iOS"),
        ("PersonaWidgets", "app-extension", "iOS"),
        ("PersonaNotify", "app-extension", "iOS"),
    ):
        target = project["targets"][name]
        if (target["type"], target["platform"]) != (kind, platform):
            raise ValueError(f"the canonical {name} target changed; review the offline graph")
    if project["packages"] != PACKAGES:
        raise ValueError("the canonical package declarations changed; review the offline graph")
    action = _offline_action(project["schemes"][SCHEME])
    if action["build"]["targets"] != {"Persona": "all", "PersonaTests": ["test"]}:
        raise ValueError("offline build must retain its canonical host and unit-test entries")
    if action["test"]["targets"] != ["PersonaTests"]:
        raise ValueError("offline test selection must remain PersonaTests")
    if action != OFFLINE_ACTION:
        raise ValueError("the canonical offline test action changed; review the offline graph")


def filtered(project, profile="full", source_root=None):
    """Keep the real host; narrow test sources only for an explicit focused profile."""
    if profile not in {"full", "focused", "ipad-diagnostic"}:
        raise ValueError(f"unknown offline test source profile {profile!r}")
    if set(project["schemes"]) != CANONICAL_SCHEMES:
        raise ValueError("the canonical project schemes changed; regenerate and review the offline graph")
    if set(project["targets"]) - set(project["schemes"]) != {
        "PersonaTests", "PersonaWidgets", "PersonaNotify", "PersonaWatchWidgets",
        "PersonaVisionWidgets", "PersonaVisionNotify", "PersonaVisionShare", "PersonaShare", "PersonaUITests",
        "AboutVersionAccessibilityUITests",
    }:
        raise ValueError("the canonical project targets changed; regenerate and review the offline graph")
    validate_canonical_graph(project)
    result = deepcopy(project)
    result["name"] = SCHEME
    result.pop("include", None)
    result["schemes"] = {SCHEME: _offline_action(project["schemes"][SCHEME])}
    result["targets"] = {name: deepcopy(target) for name, target in project["targets"].items()
                         if name not in EXCLUDED}
    if set(result["targets"]) != {"Persona", "PersonaTests", "PersonaWidgets", "PersonaNotify"}:
        raise ValueError("offline graph must retain only the real iPhone host, its extensions and unit tests")
    host = result["targets"]["Persona"]
    host["dependencies"] = [dependency for dependency in host["dependencies"]
                            if dependency.get("target") not in EXCLUDED]
    if host["dependencies"] != DEPENDENCIES["Persona"][2:]:
        raise ValueError("offline host dependencies must differ only by the Watch and Share targets")
    tests = result["targets"]["PersonaTests"]
    if tests["dependencies"] != DEPENDENCIES["PersonaTests"]:
        raise ValueError("offline tests must retain the canonical real Persona host dependency")
    settings = tests["settings"]["base"]
    if (settings["TEST_HOST"] != "$(BUILT_PRODUCTS_DIR)/Persona.app/Persona"
            or settings["BUNDLE_LOADER"] != "$(TEST_HOST)"):
        raise ValueError("offline tests must retain the real Persona host and bundle loader")
    if "PersonaUITests" in json.dumps(result):
        raise ValueError("offline graph must not contain the UI-test target")
    if profile != "full":
        tests["sources"] = focused_sources(project, source_root=source_root, html_batches=profile == "ipad-diagnostic")
        # Only the test target's source membership differs. The real host,
        # package graph, bundle loader and every test action stay canonical.
        comparison = deepcopy(result)
        comparison["targets"]["PersonaTests"]["sources"] = deepcopy(SOURCES["PersonaTests"])
        expected = filtered(project, source_root=source_root)
        if comparison != expected:
            raise ValueError("focused graph must differ from the full graph only in PersonaTests sources")
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("dump", type=Path, help="xcodegen dump --type json of the canonical whole project")
    parser.add_argument("output", type=Path)
    parser.add_argument("--profile", choices=("full", "focused", "ipad-diagnostic"), default="full")
    parser.add_argument("--source-root", type=Path, help="staged apple/PersonaTests source root")
    args = parser.parse_args()
    result = filtered(json.loads(args.dump.read_text()), profile=args.profile, source_root=args.source_root)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + "\n")


if __name__ == "__main__":
    main()
