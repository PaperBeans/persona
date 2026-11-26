import { Suspense, type ReactNode } from "react";
import { t } from "../../i18n";
import { lazyPage } from "../../lib/lazy-page";
import { Loading } from "../../ui/atoms";
import { OverlayBoundary } from "../../views/LazyOverlay";
import type { ExternalSourceConnectionsProps } from "./ExternalSourceConnections";

const Connections = lazyPage(() => import("./ExternalSourceConnections").then((module) => module.ExternalSourceConnections), { listed: false });

/** The existing repository editor renders projected PR and issue links in the same section as explicit bindings. */
export function ExternalSourcesEntry({ children, ...props }: ExternalSourceConnectionsProps & { children?: ReactNode }) {
  return (
    <section className="mt-6" aria-label={t("external-sources.section")}>
      <OverlayBoundary open onClose={() => {}} fallback={<p className="text-meta text-red" role="alert">{t("pageboundary.chunk_body")}</p>}>
        <Suspense fallback={<Loading />}><Connections key={JSON.stringify([props.manager, props.kind, props.id])} {...props} /></Suspense>
      </OverlayBoundary>
      {children}
    </section>
  );
}
