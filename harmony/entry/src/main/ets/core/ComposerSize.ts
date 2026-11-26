/** Resting phone controls follow the shell's type size, one category above the capped interface. */
export class ComposerSize {
  readonly target: number;
  readonly plus: number;
  readonly microphone: number;
  readonly promptPoints: number;
  constructor(shellBodyMetric: number, plus: number, microphone: number) {
    this.promptPoints = shellBodyMetric;
    this.target = 44 * shellBodyMetric / 17;
    this.plus = plus;
    this.microphone = microphone;
  }
}

// The columns are body points at the system category, then the shell category's
// UIFontMetrics-scaled 17pt value, title2 and headline. The interface stops at xxxLarge.
const steps: number[][] = [
  [14, 46 / 3, 20, 15], [15, 49 / 3, 21, 16], [16, 17, 22, 17], [17, 56 / 3, 24, 19],
  [19, 20, 26, 21], [21, 67 / 3, 28, 23], [23, 79 / 3, 34, 28]
];

export function restingComposerSize(fontScale: number): ComposerSize {
  const points = Math.min(23, Math.max(14, 17 * fontScale));
  let upper = 1;
  while (upper < steps.length - 1 && points > steps[upper][0]) upper++;
  const a = steps[upper - 1], b = steps[upper];
  const fraction = (points - a[0]) / (b[0] - a[0]);
  const metric = a[1] + fraction * (b[1] - a[1]);
  const title = a[2] + fraction * (b[2] - a[2]);
  const headline = a[3] + fraction * (b[3] - a[3]);
  // At the standard shell size, a 24vp Harmony microphone gives the 19pt iPhone headline icon's painted height.
  return new ComposerSize(metric, title, headline * 24 / 19);
}

// UIKit body advances change with optical size. Measure the platform's default
// font against the same lowercase alphabet and its word separator.
// The user's draft always goes through the actual platform paragraph shaper.
export const composerTypeSample = 'abcdefghijklmnopqrstuvwxyz ';
const bodyMetrics: number[][] = [
  [14,15,192.541], [15,16,204.316], [16,17,216.25], [17,19,227.076],
  [19,21,252.046], [21,23,272.456], [23,28,295.645], [28,28,358.996],
  [33,28,421.362], [40,28,508.105], [47,28,594.545], [53,28,668.349]
];

export class ComposerReadingSize {
  readonly points: number;
  readonly shellPoints: number;
  readonly sampleWidth: number;
  constructor(points: number, shellPoints: number, sampleWidth: number) {
    this.points = points;
    this.shellPoints = shellPoints;
    this.sampleWidth = sampleWidth;
  }
  // The measured body face has the same 2444 / 2048 line advance at every category.
  get line(): number { return this.points * 2444 / 2048; }
  // The shell provides a minimum line box at the default reading size. The native
  // text field uses that pitch between lines and the reading face for the last line.
  get advance(): number { return Math.max(this.line, Math.round(this.shellPoints * 2444 / 2048) - 1); }
  height(lines: number): number {
    if (lines === 1) return Math.ceil(Math.max(this.line, this.shellPoints * 2444 / 2048));
    if (this.advance > this.line) return this.line + (lines - 1) * this.advance;
    return Math.round(lines * this.line);
  }
}

export function composerReadingSize(fontScale: number): ComposerReadingSize {
  const points = Math.min(53, Math.max(14, 17 * fontScale));
  let upper = 1;
  while (upper < bodyMetrics.length - 1 && points > bodyMetrics[upper][0]) upper++;
  const a = bodyMetrics[upper - 1], b = bodyMetrics[upper];
  const t = (points - a[0]) / (b[0] - a[0]);
  return new ComposerReadingSize(points, a[1] + (b[1] - a[1]) * t, a[2] + (b[2] - a[2]) * t);
}

export class CompactDraftRoom {
  readonly page: number;
  readonly visible: number;
  readonly bottomLift: number;
  constructor(page: number, visible: number, bottomLift: number) {
    this.page = page;
    this.visible = visible;
    this.bottomLift = bottomLift;
  }
}

// The reference full-height phone has 62pt above its page and 34pt below. Its docked
// keyboard, including the candidate strip, occupies 329pt. Larger occlusions win.
// Only a docked keyboard within one standard inset of that measured height gets
// the difference. Floating keyboards, compact windows and short phones keep their own room.
export function compactDraftRoom(width: number, height: number, pane: number, top: number, bottom: number,
  keyboard: number, fullScreen: boolean, hover: boolean): CompactDraftRoom | undefined {
  if (!fullScreen || hover || !(width > 0 && width < 600 && height >= 600 && height > width && Math.abs(width - pane) < 2)) return undefined;
  const safeTop = Math.max(62, top), safeBottom = Math.max(34, bottom);
  const keyboardLift = keyboard >= 313 && keyboard < 329 ? 329 - keyboard : 0;
  const visible = height - safeTop - (keyboard > 0 ? Math.max(keyboard + keyboardLift, safeBottom) : safeBottom);
  if (visible < 160) return undefined;
  return new CompactDraftRoom(height - safeTop - safeBottom, visible, keyboard > 0 ? keyboardLift : safeBottom - bottom);
}
