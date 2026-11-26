// Turn-level chat extras (TurnImages.tsx + edge-fade.ts, PortChips.tsx + lib/port-presence.ts, TurnChanges.tsx):
// kept off the cold first screen and loaded on intent only when a turn actually has inspected images, a finished
// agent reply with a host:port link, or edited files. Outside the idle tier, so IDLE_TIER_BUDGET is unaffected.
import { Suspense, useMemo } from "react";
import { lazyPage } from "../../lib/lazy-page";
import type { TurnChangeSet } from "../../lib/turn-changes";
import type { TurnImage } from "../../lib/turn-images";

const TurnImagesBody = lazyPage(() => import("./TurnImages").then((m) => m.TurnImages));
const ReplyPortsBody = lazyPage(() => import("./PortChips").then((m) => m.ReplyPorts));
const TurnChangesBody = lazyPage(() => import("./TurnChanges").then((m) => m.TurnChanges));

/** A host with a port: matches PortChips.mightLinkPort without pulling PortChips onto the first screen. */
const HOST_PORT = /(?:\/\/[^\s/:]+|localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1?\]):\d/i;

export function LazyTurnImages(props: { images: TurnImage[]; mid: string; sid: string; cwd?: string }) {
  if (!props.images.length) return null;
  return (
    <Suspense fallback={null}>
      <TurnImagesBody {...props} />
    </Suspense>
  );
}

export function LazyReplyPorts(props: { text: string; mid: string; sid: string; cwd?: string }) {
  const mayLink = useMemo(() => Boolean(props.cwd) && HOST_PORT.test(props.text), [props.cwd, props.text]);
  if (!mayLink) return null;
  return (
    <Suspense fallback={null}>
      <ReplyPortsBody {...props} />
    </Suspense>
  );
}

export function LazyTurnChanges(props: { changes: TurnChangeSet; mid: string; sid: string; cwd?: string }) {
  if (!props.changes.files.length) return null;
  return (
    <Suspense fallback={null}>
      <TurnChangesBody {...props} />
    </Suspense>
  );
}
