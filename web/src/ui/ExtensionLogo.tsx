import { useState } from "react";
import { Icon } from "./icons";

/** Catalog artwork is public, anonymous HTTPS content, never a hub-authenticated image. */
function logoURL(value?: string): string | null {
  if (!value) return null;
  try {
    const url = new URL(value);
    return url.protocol === "https:" && !url.username && !url.password ? url.href : null;
  } catch {
    return null;
  }
}

/** Keep the caller's existing tile geometry and status tint, including when artwork fails. */
export function ExtensionLogo({ url, className, iconSize }: { url?: string; className: string; iconSize: number }) {
  const src = logoURL(url);
  // Remount on URL changes: a failed image must not prevent newer artwork from loading.
  return <span aria-hidden="true" data-extension-logo className={`relative ${className}`}>
    <LogoImage key={src} src={src} iconSize={iconSize} />
  </span>;
}

function LogoImage({ src, iconSize }: { src: string | null; iconSize: number }) {
  const [failed, setFailed] = useState(false);
  const [loaded, setLoaded] = useState(false);
  if (!src || failed) return <Icon name="extension" size={iconSize} />;
  return <>
    {!loaded && <Icon name="extension" size={iconSize} />}
    <img src={src} alt="" loading="lazy" decoding="async" referrerPolicy="no-referrer" crossOrigin="anonymous"
      className={`absolute inset-0 size-full rounded-[inherit] object-contain ${loaded ? "bg-[var(--extension-logo-backing)]" : "opacity-0"}`}
      onLoad={() => setLoaded(true)} onError={() => setFailed(true)} />
  </>;
}
