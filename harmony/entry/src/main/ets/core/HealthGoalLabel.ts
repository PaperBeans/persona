// Fit the caption to its widest word-wrapped line. The centered ring then contains
// one leading-aligned native Text, with the original localized value available to readers.
export function healthGoalWidth(label: string, maximum: number, measure: (text: string) => number): number {
  const full = measure(label);
  if (full <= 0 || !Number.isFinite(full)) return maximum;
  if (full <= maximum) return full;
  let line = '';
  let widest = 0;
  for (const word of label.split(/[ \t]+/)) {
    const next = line ? line + ' ' + word : word;
    if (line && measure(next) > maximum) {
      widest = Math.max(widest, measure(line));
      line = word;
    } else {
      line = next;
    }
  }
  return Math.min(maximum, Math.max(widest, measure(line)));
}
