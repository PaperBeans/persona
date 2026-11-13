// The live screen's Displays menu (#5685, docs/extensions/computer-use.md "Displays"): All Displays or one display of a
// multi-display Mac. ScreenView imports it once there is a choice to offer, so its words (screen-displays) stay out of the
// first screen's catalog, which every page of the app sends; the build makes the import wait for the rest of the catalog.
import { useState } from "react";
import { cx, Menu } from "../ui/atoms";
import { Icon } from "../ui/icons";
import { t } from "../i18n";
import type { ScreenDisplay } from "./displays";

/** `shown`: the display shown alone, null for all of them. The menu numbers `offered` in its order. `onOpen`: the menu
 *  opened (the page asks the Mac for its displays again). */
export default function DisplayMenu({ offered, shown, onChoose, onOpen }: { offered: ScreenDisplay[]; shown: string | null; onChoose: (id: string | null) => void; onOpen: () => void }) {
  const [at, setAt] = useState<HTMLElement | null>(null); // the button, while the menu is open
  const name = (id: string | null) => {
    const i = offered.findIndex((d) => d.id === id);
    return i < 0 ? t("screen-displays.all_displays") : t("screen-displays.display_n", { n: i + 1 });
  };
  return (
    <>
      <button
        type="button"
        onClick={(e) => {
          if (!at) onOpen();
          setAt(at ? null : e.currentTarget);
        }}
        aria-haspopup="menu"
        aria-expanded={at !== null}
        aria-label={t("screen-displays.displays_current", { current: name(shown) })}
        title={t("screen-displays.displays")}
        data-testid="screen-displays"
        className={cx(
          "touch-hit flex h-7 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-control px-2 text-[12px] font-medium text-ink-2 transition-colors hover:bg-hover hover:text-ink",
          at && "bg-hover text-ink",
        )}
      >
        <Icon name="monitor" size={14} />
        {/* a narrow bar keeps the icon only; the label says the choice */}
        <span className="hidden sm:inline">{name(shown)}</span>
        <Icon name="chevron" size={12} />
      </button>
      {at && (
        <Menu
          anchor={at}
          onClose={() => setAt(null)}
          align="right"
          width={184}
          header={t("screen-displays.displays")}
          items={[null, ...offered.map((d) => d.id)].map((id) => ({
            key: id ?? "all",
            label: name(id),
            radio: true,
            checked: shown === id,
            onSelect: () => onChoose(id),
          }))}
        />
      )}
    </>
  );
}
