// Messages typed while the agent works and not yet taken (Session.queued): the panel at the top of the composer that
// shows them is its own chunk (QueuePanel.tsx, cindy's pending queue, #5327), loaded once something is queued. This
// eager wrapper only keeps that loading boundary; draft and photo restoration stays with the intent-loaded consumers.
import { Suspense, type ReactNode } from "react";
import { lazyPage } from "../../lib/lazy-page";
import type { QueuePanelProps } from "./QueuePanel";

export type { QueueEditResult, QueuePanelProps } from "./QueuePanel";

export interface QueueStripProps extends QueuePanelProps {
  /** Controlled panel renderer skips loading the default lazy QueuePanel chunk. */
  renderPanel?: (props: QueuePanelProps) => ReactNode;
}

// Warm this chunk when a chat can queue a send, or load it on demand for an existing queue.
const QueuePanel = lazyPage(() => import("./QueuePanel").then((m) => m.QueuePanel));
export const preloadQueuePanel = QueuePanel.preload;

/** The queued messages at the top of the composer (QueuePanel.tsx), drawn once its chunk is in. */
export function QueueStrip({ renderPanel, ...props }: QueueStripProps) {
  if (renderPanel) return <>{renderPanel(props)}</>;
  return (
    <Suspense fallback={null}>
      <QueuePanel {...props} />
    </Suspense>
  );
}
