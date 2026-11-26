// HarmonyOS publishes these FONT_SCALE values for standard and accessibility sizes.
const fontSteps: number[] = [0.85,1,1.15,1.3,1.45,1.75,2,3.2];

export function phoneTextScale(system: number, choice: string): number {
  const base = Number.isFinite(system) && system > 0 ? Math.min(3.2,system) : 1;
  if (choice === 'small') {
    for (let i=fontSteps.length-1;i>=0;i--) if (fontSteps[i] < base-0.001) return fontSteps[i];
    return Math.min(base,fontSteps[0]);
  }
  if (choice === 'large') {
    for (const next of fontSteps) if (next > base+0.001) return next;
    return 3.2;
  }
  return base;
}
