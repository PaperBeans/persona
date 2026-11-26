// The iPhone briefing uses up to two nonzero units, rounded to the nearest even last unit.
function roundedUnits(seconds: number, unit: number): number {
  const lower = Math.floor(seconds / unit);
  const remainder = seconds - lower * unit;
  if (remainder * 2 === unit) return lower + lower % 2;
  return remainder * 2 > unit ? lower + 1 : lower;
}

export function briefWaitDuration(seconds: number, language: string, days: string, hours: string, minutes: string): string {
  const elapsed = Math.max(60, Number.isFinite(seconds) ? seconds : 0);
  let total = roundedUnits(elapsed, 60);
  if (total >= 1440 && total % 1440 >= 60) total = roundedUnits(elapsed, 3600) * 60;
  const parts: string[] = [];
  const day = Math.floor(total / 1440), hour = Math.floor(total % 1440 / 60), minute = total % 60;
  if (day) parts.push(days.replace('{count}', String(day)));
  if (hour) parts.push(hours.replace('{count}', String(hour)));
  if (minute) parts.push(minutes.replace('{count}', String(minute)));
  return parts.join(/^(zh|ja)(-|_|$)/i.test(language) ? '' : ' ');
}
