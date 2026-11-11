# Icons by meaning

`meanings.json` maps the iPhone's icon meanings (`Theme.Icon` in `apple/Shared/Design/Icons.swift`) to the glyphs other
clients draw for them, so one meaning keeps one icon everywhere (#4209). Each entry is keyed by the `Theme.Icon` name and
holds its SF Symbol (`sf`, exactly as in `Icons.swift`) and the Phosphor glyph Android draws (`phosphor`, kebab case, one
of the names `scripts/assets/gen_phosphor_icons.py` generates into `PhIcons`).

- Only meanings with a fitting glyph are listed; a meaning without one stays out rather than taking a loose match.
- Several meanings may share a glyph.
- `scripts/assets/gen_icon_meanings.py` writes Android's `PersonaIcons` from it (`--check` in CI and the pre-commit hook);
  `scripts/assets/test_gen_icon_meanings.py` checks every name and symbol against `Icons.swift` and every glyph against
  the Phosphor list.
- A new glyph is a local step: add its name to `gen_phosphor_icons.py` and run it against a Phosphor checkout, since CI
  has none.

It lives outside `spec/tokens` because the token generator reads every JSON file there.
