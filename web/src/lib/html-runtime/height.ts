// An html component's height (the schema's bounds and default; #4215). Its own module: the card's frame on the first
// screen sizes its placeholder with it, and nothing else of the runtime should come along.

export const HEIGHT_MIN = 160;
export const HEIGHT_MAX = 640;
export const HEIGHT_DEFAULT = 320;

export const cardHeight = (h: unknown) => (typeof h === "number" && Number.isFinite(h) ? Math.min(HEIGHT_MAX, Math.max(HEIGHT_MIN, Math.round(h))) : HEIGHT_DEFAULT);
