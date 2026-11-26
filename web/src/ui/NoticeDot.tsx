import { cx } from "./atoms";

/** A passive navigation reminder. The enclosing link or button supplies its spoken label. */
export function NoticeDot({ className }: { className?: string }) {
  return <span aria-hidden className={cx("inline-block size-1.5 shrink-0 rounded-full bg-accent", className)} />;
}
