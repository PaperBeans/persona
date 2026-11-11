// Shared sizing and recipes for measured, variable-height session rows.
export const SESSION_LIST_METRICS = { estimate: 44, overscan: 6, searchDelay: 250, groupOptions: 50, avatar: 22, icon: 12 } as const;
export const SESSION_LIST_STYLE = {
  foldButton: "touch-hit relative z-10 flex size-6 shrink-0 items-center justify-center rounded-control text-ink-3 hover:bg-hover hover:text-ink-2 [@media(pointer:coarse)]:size-11",
  heading: "mt-3 flex items-center gap-1 px-2",
  headingText: "min-w-0 flex-1 text-[10.5px] font-medium tracking-[0.06em] text-ink-3 uppercase",
  personalPage: "mx-auto max-w-[760px] px-5 py-6 sm:px-8",
  personalRow: "flex items-center gap-2 px-4 py-2 hover:bg-hover",
  personalTitle: "min-w-0 flex-1 truncate text-[13px] text-ink",
  personalBadge: "rounded-full bg-accent px-1.5 text-[10.5px] font-semibold text-white tabular-nums",
  pinButton: "flex size-6 shrink-0 items-center justify-center rounded-control text-ink-3 hover:bg-hover",
} as const;
