// Markdown images on a third-party host stay unloaded until the reader taps them (#8162): opening a conversation must
// not send the reader's address to a host the message names. The hub's own images load inline as before. The gate is
// decided from the resolved URL alone, so streaming and cached blocks classify it the same way.
import { useState, type ComponentProps } from "react";
import { isOwnImageURL } from "../lib/links";
import { t } from "../i18n";
import { Icon } from "./icons";

type GateState = "idle" | "loading" | "failed";

function hostOf(src: string): string {
  try {
    // Same page-origin base as isOwnImageURL, so a protocol-relative //host/x labels the host instead of the raw string.
    return new URL(src, typeof location !== "undefined" ? location.href : undefined).host;
  } catch {
    return src;
  }
}

const gateClass =
  "my-0.5 inline-flex items-center gap-1.5 rounded-chip bg-inset px-2.5 py-1 text-[12.5px] text-ink-2 shadow-btn hover:bg-hover hover:text-ink";

export function ExternalImage({ node: _node, src = "", alt = "", ...rest }: ComponentProps<"img"> & { node?: unknown }) {
  const [gate, setGate] = useState<GateState>("idle");
  if (isOwnImageURL(src)) return <img src={src} alt={alt} {...rest} />;
  if (gate !== "loading") {
    const host = hostOf(src);
    return (
      <button
        type="button"
        className={gateClass}
        title={alt || src}
        data-external-image={gate === "failed" ? "retry" : "gate"}
        onClick={() => setGate("loading")}
      >
        <Icon name="image" size={14} />
        <span>{t(gate === "failed" ? "ui-markdown.image_retry" : "ui-markdown.image_load", { host })}</span>
      </button>
    );
  }
  // The src reaches an <img> only after the tap, and the request carries no Referer header.
  return <img {...rest} src={src} alt={alt} referrerPolicy="no-referrer" onError={() => setGate("failed")} data-external-image="loading" />;
}
