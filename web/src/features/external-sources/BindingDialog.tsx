import { Suspense } from "react";
import { t } from "../../i18n";
import { lazyPage } from "../../lib/lazy-page";
import { Button, Dialog, Loading } from "../../ui/atoms";
import { OverlayBoundary } from "../../views/LazyOverlay";
import type { ExternalSourceBindingEditorProps } from "./ExternalSourceBindingEditor";

const Editor = lazyPage(() => import("./ExternalSourceBindingEditor").then((module) => module.ExternalSourceBindingEditor), { listed: false });

export function BindingDialog(props: ExternalSourceBindingEditorProps) {
  const { onClose } = props;
  const pending = (failed: boolean) => (
    <Dialog open title={t(props.binding ? "external-sources.edit_binding" : "external-sources.add_binding")} onClose={onClose}
      footer={<Button tone="quiet" onClick={onClose}>{t("common.cancel")}</Button>}>
      {failed ? <p role="alert" className="text-meta text-red">{t("pageboundary.chunk_body")}</p> : <Loading />}
    </Dialog>
  );
  return (
    <OverlayBoundary open onClose={onClose} fallback={pending(true)}>
      <Suspense fallback={pending(false)}><Editor {...props} /></Suspense>
    </OverlayBoundary>
  );
}
