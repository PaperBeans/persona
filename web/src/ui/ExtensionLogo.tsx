import { useEffect, useRef, useState, useSyncExternalStore, type RefObject } from "react";
import { credentialOwner, subscribeCredentialOwner } from "../lib/api";
import { acquireExtensionLogo, extensionLogoSource } from "../lib/extension-logo";
import { defaultManager, managerHeaderFor, onDefaultManager, useChosenManager } from "../lib/manager-choice";
import { Icon } from "./icons";

const subscribeDefault = (listener: () => void) => onDefaultManager(listener);

/** Keep the caller's existing tile geometry and status tint, including when artwork fails. */
export function ExtensionLogo({ url, manager, className, iconSize }: { url?: string; manager?: string; className: string; iconSize: number }) {
  const source = extensionLogoSource(url);
  const owner = useSyncExternalStore(subscribeCredentialOwner, credentialOwner);
  useChosenManager();
  const fallbackOwner = useSyncExternalStore(subscribeDefault, defaultManager);
  const resolvedManager = (manager ?? (source?.path ? managerHeaderFor(source.path) : undefined)) || fallbackOwner;
  const tile = useRef<HTMLSpanElement>(null);
  // Remount on URL/owner changes: failed or retired artwork cannot replace a new request.
  return <span ref={tile} aria-hidden="true" data-extension-logo className={`relative ${className}`}>
    {source?.path ? <HubLogo key={`${owner}:${resolvedManager}:${source.path}`} path={source.path} manager={resolvedManager} tile={tile} iconSize={iconSize} />
      : <LogoImage key={source?.publicURL} src={source?.publicURL ?? null} iconSize={iconSize} />}
  </span>;
}

function HubLogo({ path, manager, tile, iconSize }: { path: string; manager?: string; tile: RefObject<HTMLSpanElement | null>; iconSize: number }) {
  const [src, setSrc] = useState<string | null>(null);
  useEffect(() => {
    let active = true;
    let objectURL: string | undefined;
    let request: ReturnType<typeof acquireExtensionLogo> | undefined;
    const start = () => {
      if (!active || request) return;
      request = acquireExtensionLogo(path, manager);
      request.promise.then(blob => {
        if (!active) return;
        objectURL = URL.createObjectURL(blob);
        setSrc(objectURL);
      }).catch(() => { /* Keep the existing generic mark on unavailable artwork. */ });
    };
    // loading="lazy" on the eventual <img> cannot defer the preceding authenticated fetch.
    // Observe the existing tile with the default zero margin, including its scrolling ancestors' clipping.
    let observer: IntersectionObserver | undefined;
    if (typeof IntersectionObserver !== "undefined" && tile.current) {
      observer = new IntersectionObserver(entries => {
        if (entries.some(entry => entry.isIntersecting)) { observer?.disconnect(); start(); }
      });
      observer.observe(tile.current);
    } else start();
    return () => {
      active = false;
      observer?.disconnect();
      request?.release();
      if (objectURL) URL.revokeObjectURL(objectURL);
    };
  }, [path, manager, tile]);
  return <LogoImage key={src} src={src} iconSize={iconSize} />;
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
