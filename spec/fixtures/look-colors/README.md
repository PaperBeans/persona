# look-colors

Automatic theme color seed extraction for uploaded avatars (#15449, `docs/apple/persona.md` "Uploading an image"). The hub (Go), web client (TypeScript) and Apple clients (Swift) all compute the automatic seed from frame 0 of the main avatar picture so the client's live preview matches what the hub writes into `persona.json`.

## Method

Based on `scripts/assets/make_persona.py` (`extract_colors`), pinned to integer channel math so Go, TypeScript and Swift produce bit-exact identical `#RRGGBB` results on every image:

1. **Input pixels**: decode the image to straight (non-premultiplied) 8-bit sRGB RGBA tuples `(R, G, B, A)`, each in `0..255`. (Clients that render into a premultiplied canvas or bitmap context must read straight RGBA or un-premultiply pixels with `200 < A < 255` before applying the rule.)
2. **Filter pixels**: keep a pixel `(R, G, B, A)` if and only if all three hold:
   - `A > 200`
   - Let `mx = max(R, G, B)`, `mn = min(R, G, B)`, `d = mx - mn`.
   - Value: `mx / 255.0 > 0.35` (identically `mx >= 90` for all 8-bit integers).
   - Saturation: `d / mx > 0.3` (identically `10 * d > 3 * mx` for all integers `1 <= d <= mx <= 255`).
3. **Gray fallback**: if fewer than `50` pixels are kept, return `#787878` (`(120, 120, 120)`).
4. **Hue bin (`0..35`)**: for each kept pixel (`d > 0` is guaranteed by `10 * d > 3 * mx`), assign it to a 10-degree hue bin `b` in `0..35`:
   - If `mx == R`: `num = (G - B)` if `G >= B` else `(G - B) + 6 * d`
   - Else if `mx == G`: `num = (B - R) + 2 * d`
   - Else (`mx == B`): `num = (R - G) + 4 * d`
   - `b = (6 * num) // d % 36` (integer floor division of non-negative `6 * num` by `d`, modulo 36; equivalent to `floor(hue_degrees / 10) % 36` with no floating-point boundary drift).
5. **Peak bin `p`**: count kept pixels in each bin `counts[0..35]`. Pick the bin index `p` with the highest count; ties break to the lowest index `0..35`.
6. **Neighborhood**: collect all kept pixels whose bin is in `{(p + 35) % 36, p, (p + 1) % 36}` (wrapping across 0°/360°). Let `N` be the number of collected pixels (`N >= 1`).
7. **Lower-middle channel median**: for each channel `R`, `G` and `B` independently, sort its `N` integer values (`0..255`) in ascending order and pick the **lower middle** element at 0-based index `(N - 1) // 2` (no averaging of the two middle values on even `N`, no float rounding).
8. **Output**: format the three integer channel values as uppercase `#RRGGBB`.

## Fixtures

All PNGs are tiny synthetic 16×16 RGBA images generated with no real photos:

- `solid-blue.png`: uniform `#3A7BD5` opaque pixels → `#3A7BD5`.
- `two-hues.png`: dominant warm amber-orange across adjacent bins (even pixel count with two distinct shades so averaging or upper-middle would differ) plus a smaller teal region → `#E68C28`.
- `transparent-and-dark.png`: mixes transparent (`A = 0` and `A = 200`), dark (`mx = 89`, `V <= 0.35`) and low-saturation (`10 * d <= 3 * mx`, `S <= 0.3`) pixels with 64 qualifying green pixels → `#32B464`.
- `gray-fallback.png`: neutral grays plus 49 colorful pixels (one short of the 50-pixel minimum) → `#787878`.
- `hue-wrap.png`: reds spanning bins `35`, `0` and `1` across the 0°/360° hue boundary → `#DC3228`.

`cases.json` lists each case (`name`, `description`, `file`, `expected_seed`) under `{"version": 1, "cases": [...]}`.

## Checked by

- Hub (Go), web (TypeScript) and Apple (Swift) tests replay `cases.json` against the PNGs in this directory.
