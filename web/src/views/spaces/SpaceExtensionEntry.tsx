// Compatibility entry points for the optional Space Conversations extension.
// No built-in domain-space navigation is mounted without the extension.
import { useEffect } from "react";
import { useExtensionPlace } from "../../lib/extension-slots";
import { navigate } from "../../lib/router";
import { Empty } from "../assistant/parts";
import { Button, Loading } from "../../ui/atoms";
import { t } from "../../i18n";

export function SpaceExtensionEntry({ slug, fresh }: { slug?: string; fresh?: boolean }) {
  const place = useExtensionPlace("space-conversations", "home");
  useEffect(() => {
    if (!place) return;
    const ui = fresh ? "new" : slug ? "space" : "home";
    const state = ui === "space" ? `?state=${encodeURIComponent(slug ?? "")}` : "";
    navigate(`/extension/space-conversations/${ui}${state}`, true);
  }, [place, slug, fresh]);
  if (place) return <div className="flex h-full items-center justify-center"><Loading /></div>;
  return (
    <div className="flex h-full flex-col justify-center pb-[10vh]">
      <Empty icon="layers" title={t("spaces.extension_required_title")} action={<Button icon="extension" onClick={() => navigate("/settings?tab=extensions")}>{t("spaces.extension_install")}</Button>}>
        {t("spaces.extension_required_body")}
      </Empty>
    </div>
  );
}
