import { lazy, Suspense } from "react";

// Keep the execution query and tooltip out of the shared startup bundle.
const Badge = lazy(() => import("./MainRuntimeBadge").then((m) => ({ default: m.MainRuntimeBadge })));

export function MainRuntimeSlot(props: { sid?: string; starting?: boolean }) {
  return <Suspense fallback={null}><Badge {...props} /></Suspense>;
}
