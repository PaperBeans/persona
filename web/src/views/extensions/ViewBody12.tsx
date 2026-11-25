// UI version 12 (#14730, docs/extensions/extensions.md "Declarative UI"): interactive offline vector `map`
// component — cosine-corrected equirectangular projection, multi-route selection, draggable/zoomable camera
// persisted per (owner, key), metric scale bar, marker inspection and actions, optional companion stop list,
// verified HTTPS links, and Full screen modal overlay. Own lazy chunk (loaded via ViewBody.tsx on demand).
import { useCallback, useContext, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { t } from "../../i18n";
import { useCloseOnBack } from "../../lib/back-close";
import { ownerContext } from "../../lib/extension-owner";
import {
  readMap, type UIAction, type UIItem, type UIMap, type UIMapCoordinate, type UIMapLink,
  type UIMapMarker, type UIMapRoute, type UIMapTravelMode,
} from "../../lib/extension-ui";
import { colorOf, hue } from "../../lib/extension-ui-v6";
import { linkHref, openLink, opensInApp } from "../../lib/links";
import { useLinkManager } from "../../lib/manager-choice";
import { cx, IconButton, Spinner } from "../../ui/atoms";
import { Icon, type IconName } from "../../ui/icons";
import { useEscapeClose, useModalFocus } from "../../ui/overlay";
import { ROW_ICON } from "./ViewBody";
import { glyphOf } from "./ViewBody6";
import "./ViewBody12.css";

const METERS_PER_DEG_LAT = 111_320;
const MIN_SPAN_METERS = 450;
const STEP = 1.4;
const KEY_PAN_PX = 48;
const DEFAULT_STAGE_W = 640;
const DEFAULT_STAGE_H = 360;
const DEFAULT_PREVIEW_H = 200;
const SCALE_STEPS = [
  10, 20, 50, 100, 200, 500,
  1_000, 2_000, 5_000, 10_000, 20_000, 50_000,
  100_000, 200_000, 500_000, 1_000_000, 2_000_000, 5_000_000,
];

interface Camera {
  cx: number;
  cy: number;
  scale: number;
}

interface ProjectedFrame {
  refLat: number;
  refLon: number;
  cosLat: number;
  project: (c: UIMapCoordinate) => { x: number; y: number };
  unproject: (x: number, y: number) => UIMapCoordinate;
  fitBounds: (selectedRouteId: string | undefined) => {
    cx: number;
    cy: number;
    spanW: number;
    spanH: number;
  };
  worldExtent: { minX: number; maxX: number; minY: number; maxY: number };
}

interface MapStore {
  selectedRouteId?: string;
  selectedMarkerId?: string;
  camera?: Camera;
  moved: boolean;
}

const STORES = new Map<string, MapStore>();
const STORE_CAP = 128;
const HELD = new Map<string, number>();

const evictStore = () => {
  for (const id of STORES.keys()) {
    if (!HELD.get(id)) {
      STORES.delete(id);
      return;
    }
  }
};

const storeFor = (scope: string, key: string): MapStore => {
  const id = `${scope}\u0000${key}`;
  let s = STORES.get(id);
  if (s) {
    STORES.delete(id);
    STORES.set(id, s);
    return s;
  }
  if (STORES.size >= STORE_CAP) evictStore();
  s = { moved: false };
  STORES.set(id, s);
  return s;
};

/** Clears cached map state across keys (test isolation; application code never calls this). */
export const forgetMapStores = () => STORES.clear();

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

const TONE_CSS: Record<string, string> = {
  positive: "var(--green)",
  caution: "var(--orange)",
  danger: "var(--red)",
};

function resolveColor(tone?: string, color?: string, fallback = "var(--accent)"): string {
  if (tone && Object.hasOwn(TONE_CSS, tone)) return TONE_CSS[tone];
  const named = colorOf(color);
  return named ? hue(named) : fallback;
}

function markerColor(m: UIMapMarker): string {
  const roleDefault = m.kind === "origin" ? "var(--green)" : m.kind === "user" ? "var(--hue-blue)" : "var(--accent)";
  return resolveColor(m.tone, m.color, roleDefault);
}

function markerGlyph(m: UIMapMarker): IconName {
  if (m.icon) {
    const mapped = glyphOf(m.icon, ROW_ICON);
    if (mapped) return mapped;
  }
  if (m.kind === "origin") return "home";
  return "location";
}

function ModeGlyph({
  mode,
  size,
  className,
  style,
}: {
  mode?: UIMapTravelMode;
  size: number;
  className?: string;
  style?: React.CSSProperties;
}) {
  if (mode === "driving") return <Icon name="car" size={size} className={className} style={style} />;
  if (mode !== "walking" && mode !== "transit" && mode !== "cycling") {
    return <Icon name="map" size={size} className={className} style={style} />;
  }
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.9}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      className={className}
      style={style}
    >
      {mode === "walking" && (
        <g>
          <circle cx="12" cy="4" r="1.5" />
          <path d="M13 8.5 9.5 12l2.5 2-1 6M13 8.5l3 3.5-2.5 2 2.5 6" />
        </g>
      )}
      {mode === "transit" && (
        <g>
          <rect x="5" y="3" width="14" height="14" rx="3" />
          <path d="M5 11h14M9 17l-2 4m10-4 2 4M8.5 14h.01M15.5 14h.01" />
        </g>
      )}
      {mode === "cycling" && (
        <g>
          <circle cx="5.5" cy="17.5" r="3.5" />
          <circle cx="18.5" cy="17.5" r="3.5" />
          <path d="M15 6a1 1 0 1 0 0-2 1 1 0 0 0 0 2zM12 17.5V14l-3-3 4-3 2 3h2" />
        </g>
      )}
    </svg>
  );
}

function modeLabel(mode?: UIMapTravelMode): string {
  switch (mode) {
    case "driving":
      return t("extensions-viewbody.map_mode_driving");
    case "walking":
      return t("extensions-viewbody.map_mode_walking");
    case "transit":
      return t("extensions-viewbody.map_mode_transit");
    case "cycling":
      return t("extensions-viewbody.map_mode_cycling");
    default:
      return t("extensions-viewbody.map");
  }
}

export function formatMapDistance(meters: number): string {
  if (meters >= 1000) {
    const km = meters / 1000;
    const value = km >= 10 ? String(Math.round(km)) : String(Number(km.toFixed(1)));
    return t("extensions-viewbody.map_distance_km", { value });
  }
  return t("extensions-viewbody.map_distance_m", { value: Math.round(meters) });
}

export function formatMapDuration(seconds: number): string {
  const mins = Math.max(1, Math.round(seconds / 60));
  if (mins >= 60) {
    const hours = Math.floor(mins / 60);
    const minutes = mins % 60;
    return t("extensions-viewbody.map_duration_hm", { hours, minutes });
  }
  return t("extensions-viewbody.map_duration_m", { minutes: mins });
}

function routeTitle(route: UIMapRoute, index: number): string {
  if (route.label?.trim()) return route.label;
  if (route.mode) return modeLabel(route.mode);
  return t("extensions-viewbody.map_route_numbered", { index: index + 1 });
}

function routeSummaryMeta(route: UIMapRoute, tabTitle?: string): string {
  const parts: string[] = [];
  if (route.mode) {
    const label = modeLabel(route.mode);
    if (!tabTitle || label.trim().toLowerCase() !== tabTitle.trim().toLowerCase()) parts.push(label);
  }
  if (route.duration_seconds !== undefined && route.duration_seconds > 0) parts.push(formatMapDuration(route.duration_seconds));
  if (route.distance_meters !== undefined && route.distance_meters >= 0) parts.push(formatMapDistance(route.distance_meters));
  return parts.join(" · ");
}

function defaultRouteId(routes: readonly UIMapRoute[]): string | undefined {
  return routes.find((r) => r.selected)?.id ?? routes[0]?.id;
}

function buildFrame(map: UIMap): ProjectedFrame {
  const coords: UIMapCoordinate[] = [];
  for (const m of map.markers ?? []) coords.push({ lat: m.lat, lon: m.lon });
  for (const r of map.routes ?? []) {
    for (const p of r.points) coords.push(p);
  }
  if (!coords.length && map.viewport) coords.push(map.viewport.center);

  const lats = coords.map((c) => c.lat);
  const lons = coords.map((c) => c.lon);
  const minLat = lats.length ? Math.min(...lats) : 0;
  const maxLat = lats.length ? Math.max(...lats) : 0;
  const minLon = lons.length ? Math.min(...lons) : 0;
  const maxLon = lons.length ? Math.max(...lons) : 0;

  const refLat = map.viewport && !coords.length ? map.viewport.center.lat : (minLat + maxLat) / 2;
  const refLon = map.viewport && !coords.length ? map.viewport.center.lon : (minLon + maxLon) / 2;
  const cosLat = Math.max(0.15, Math.cos((clamp(refLat, -85, 85) * Math.PI) / 180));

  const project = (c: UIMapCoordinate) => ({
    x: (c.lon - refLon) * cosLat * METERS_PER_DEG_LAT,
    y: -(c.lat - refLat) * METERS_PER_DEG_LAT,
  });

  const unproject = (x: number, y: number): UIMapCoordinate => ({
    lat: clamp(refLat - y / METERS_PER_DEG_LAT, -90, 90),
    lon: clamp(refLon + x / (cosLat * METERS_PER_DEG_LAT), -180, 180),
  });

  const allProjected = coords.map(project);
  const worldExtent = allProjected.length
    ? {
        minX: Math.min(...allProjected.map((p) => p.x)),
        maxX: Math.max(...allProjected.map((p) => p.x)),
        minY: Math.min(...allProjected.map((p) => p.y)),
        maxY: Math.max(...allProjected.map((p) => p.y)),
      }
    : { minX: -MIN_SPAN_METERS, maxX: MIN_SPAN_METERS, minY: -MIN_SPAN_METERS, maxY: MIN_SPAN_METERS };

  const fitBounds = (selectedRouteId: string | undefined) => {
    if (!selectedRouteId && map.viewport) {
      const vpCenter = project(map.viewport.center);
      const spanLatDeg = Math.max(0.004, map.viewport.span_lat ?? 0.004);
      const spanLonDeg = Math.max(0.004, map.viewport.span_lon ?? 0.004);
      return {
        cx: vpCenter.x,
        cy: vpCenter.y,
        spanW: Math.max(MIN_SPAN_METERS, spanLonDeg * cosLat * METERS_PER_DEG_LAT),
        spanH: Math.max(MIN_SPAN_METERS, spanLatDeg * METERS_PER_DEG_LAT),
      };
    }

    const routes = map.routes ?? [];
    const chosen = selectedRouteId ? routes.filter((r) => r.id === selectedRouteId) : routes;
    const targetRoutes = chosen.length ? chosen : routes;
    const pts: { x: number; y: number }[] = [];
    for (const m of map.markers ?? []) pts.push(project({ lat: m.lat, lon: m.lon }));
    for (const r of targetRoutes) {
      for (const p of r.points) pts.push(project(p));
    }
    if (!pts.length && map.viewport) {
      const c = project(map.viewport.center);
      const spanLatDeg = Math.max(0.004, map.viewport.span_lat ?? 0.004);
      const spanLonDeg = Math.max(0.004, map.viewport.span_lon ?? 0.004);
      return {
        cx: c.x,
        cy: c.y,
        spanW: Math.max(MIN_SPAN_METERS, spanLonDeg * cosLat * METERS_PER_DEG_LAT),
        spanH: Math.max(MIN_SPAN_METERS, spanLatDeg * METERS_PER_DEG_LAT),
      };
    }
    const xs = pts.map((p) => p.x);
    const ys = pts.map((p) => p.y);
    const x0 = Math.min(...xs);
    const x1 = Math.max(...xs);
    const y0 = Math.min(...ys);
    const y1 = Math.max(...ys);
    return {
      cx: (x0 + x1) / 2,
      cy: (y0 + y1) / 2,
      spanW: Math.max(MIN_SPAN_METERS, x1 - x0),
      spanH: Math.max(MIN_SPAN_METERS, y1 - y0),
    };
  };

  return { refLat, refLon, cosLat, project, unproject, fitBounds, worldExtent };
}

function fitCamera(
  frame: ProjectedFrame,
  stageW: number,
  stageH: number,
  pad: number,
  selectedRouteId?: string,
): Camera {
  const b = frame.fitBounds(selectedRouteId);
  const availW = Math.max(40, stageW - pad * 2);
  const availH = Math.max(40, stageH - pad * 2);
  const scale = clamp(Math.min(availW / b.spanW, availH / b.spanH), 1e-6, 4);
  return { cx: b.cx, cy: b.cy, scale };
}

function niceScaleBar(scalePxPerM: number): { meters: number; px: number; label: string } {
  const safeScale = Math.max(1e-6, scalePxPerM);
  let best = SCALE_STEPS[0];
  let bestDiff = Infinity;
  for (const step of SCALE_STEPS) {
    const px = step * safeScale;
    if (px >= 44 && px <= 120) {
      best = step;
      bestDiff = 0;
      break;
    }
    const diff = px < 44 ? 44 - px : px - 120;
    if (diff < bestDiff) {
      bestDiff = diff;
      best = step;
    }
  }
  return {
    meters: best,
    px: Math.round(clamp(best * safeScale, 28, 128)),
    label: formatMapDistance(best),
  };
}

function gridStepMeters(spanMeters: number): number {
  const raw = Math.max(50, spanMeters / 4);
  const exp = 10 ** Math.floor(Math.log10(raw));
  const frac = raw / exp;
  const nice = frac >= 5 ? 5 : frac >= 2 ? 2 : 1;
  return nice * exp;
}

function MarkerPinBadge({ marker, selected }: { marker: UIMapMarker; selected: boolean }) {
  const color = markerColor(marker);
  if (marker.kind === "origin") {
    return (
      <span
        aria-hidden
        className={cx(
          "block size-3.5 rounded-full border-2 shadow-card transition-transform",
          selected ? "scale-110 border-ink ring-2 ring-accent" : "border-surface",
        )}
        style={{ background: color }}
      />
    );
  }
  const glyph = markerGlyph(marker);
  return (
    <span
      aria-hidden
      className={cx(
        "flex size-7 items-center justify-center rounded-full border-2 text-white shadow-card transition-transform",
        selected ? "scale-110 border-ink ring-2 ring-accent" : "border-surface",
      )}
      style={{ background: color }}
    >
      <Icon name={glyph} size={13} stroke={2} />
    </span>
  );
}

/**
 * Interactive vector map component (`type: "map"`, UI version 12):
 * renders offline cosine-corrected equirectangular geometry (routes, markers, graticule, metric scale bar),
 * route selector pills/tabs, stop list, marker details and push actions, HTTPS links, and full-screen modal.
 */
export function MapCanvas({
  item,
  compact,
  busy,
  onPress,
  canPress,
}: {
  item: UIItem;
  compact?: boolean;
  busy: string;
  onPress: (a: UIAction, destructive?: boolean) => void;
  canPress?: (a: UIAction) => boolean;
}) {
  const parsed = useMemo(() => readMap(item), [item]);
  const scoped = useContext(ownerContext) ?? (typeof location !== "undefined" ? location.pathname : "");
  const keyed = typeof item.key === "string" && item.key ? item.key : null;
  const linkManager = useLinkManager();

  const instanceStore = useRef<MapStore | null>(null);
  if (!instanceStore.current) instanceStore.current = { moved: false };
  const storeKey = `${scoped}\u0000${keyed ?? ""}`;

  useLayoutEffect(() => {
    if (keyed === null) return;
    HELD.set(storeKey, (HELD.get(storeKey) ?? 0) + 1);
    return () => {
      const left = (HELD.get(storeKey) ?? 1) - 1;
      if (left > 0) HELD.set(storeKey, left);
      else HELD.delete(storeKey);
    };
  }, [storeKey, keyed]);

  const [full, setFull] = useState(false);
  const still = Boolean(compact) && !full;
  const readStore = (): MapStore => (!still && keyed !== null ? storeFor(scoped, keyed) : instanceStore.current!);

  const opener = useRef<HTMLButtonElement | null>(null);
  const dialogRef = useRef<HTMLDivElement | null>(null);
  const stageRef = useRef<HTMLDivElement | null>(null);
  const wasFull = useRef(false);
  if (full) wasFull.current = true;
  useLayoutEffect(() => {
    if (full || !wasFull.current) return;
    wasFull.current = false;
    if ((!document.activeElement || document.activeElement === document.body) && opener.current?.isConnected) {
      opener.current.focus({ preventScroll: true });
    }
  }, [full]);

  useModalFocus(dialogRef, full);
  useEscapeClose(full, () => setFull(false));
  useCloseOnBack(full, () => setFull(false));

  const routes = parsed?.routes ?? [];
  const markers = parsed?.markers ?? [];
  const links: UIMapLink[] = parsed?.links ?? [];
  const altText = parsed?.alt || item.alt || item.title || t("extensions-viewbody.map");
  const isStatic = Boolean(parsed?.static ?? item.static);

  const frame = useMemo(() => (parsed ? buildFrame(parsed) : null), [parsed]);

  const initialRoute = defaultRouteId(routes);
  const [selectedRouteId, setSelectedRouteId] = useState<string | undefined>(() => {
    if (!still && keyed !== null) {
      const kept = storeFor(scoped, keyed).selectedRouteId;
      if (kept && routes.some((r) => r.id === kept)) return kept;
    }
    return initialRoute;
  });
  const [selectedMarkerId, setSelectedMarkerId] = useState<string | undefined>(() => {
    if (!still && keyed !== null) {
      const kept = storeFor(scoped, keyed).selectedMarkerId;
      if (kept && markers.some((m) => m.id === kept)) return kept;
    }
    return undefined;
  });

  // Sync route/marker selections when store key or items change.
  const activeRouteId = selectedRouteId && routes.some((r) => r.id === selectedRouteId) ? selectedRouteId : initialRoute;
  const activeRoute = routes.find((r) => r.id === activeRouteId);
  const activeMarkerId = selectedMarkerId && markers.some((m) => m.id === selectedMarkerId) ? selectedMarkerId : undefined;
  const activeMarker = activeMarkerId ? markers.find((m) => m.id === activeMarkerId) : undefined;

  const [stageSize, setStageSize] = useState<{ w: number; h: number }>({
    w: DEFAULT_STAGE_W,
    h: still ? DEFAULT_PREVIEW_H : DEFAULT_STAGE_H,
  });

  const [moved, setMoved] = useState<boolean>(() => (!still && keyed !== null ? storeFor(scoped, keyed).moved : false));
  const [camera, setCamera] = useState<Camera>(() => {
    if (!frame) return { cx: 0, cy: 0, scale: 1 };
    if (!still && keyed !== null) {
      const kept = storeFor(scoped, keyed);
      if (kept.moved && kept.camera) return kept.camera;
    }
    return fitCamera(frame, DEFAULT_STAGE_W, still ? DEFAULT_PREVIEW_H : DEFAULT_STAGE_H, still ? 24 : 44, undefined);
  });

  // Reset per-keyed-store state cleanly when switching between owners or map keys on mounted component.
  const prevStoreKey = useRef(storeKey);
  useLayoutEffect(() => {
    if (prevStoreKey.current === storeKey) return;
    prevStoreKey.current = storeKey;
    if (still || keyed === null) return;
    const kept = storeFor(scoped, keyed);
    setMoved(kept.moved);
    if (kept.selectedRouteId && routes.some((r) => r.id === kept.selectedRouteId)) setSelectedRouteId(kept.selectedRouteId);
    else setSelectedRouteId(initialRoute);
    if (kept.selectedMarkerId && markers.some((m) => m.id === kept.selectedMarkerId)) setSelectedMarkerId(kept.selectedMarkerId);
    else setSelectedMarkerId(undefined);
    if (kept.moved && kept.camera) setCamera(kept.camera);
    else if (frame) setCamera(fitCamera(frame, stageSize.w, stageSize.h, 44, undefined));
    // eslint-disable-next-line react-hooks/exhaustive-deps -- resync strictly when (owner, key) changes
  }, [storeKey]);

  const cameraRef = useRef(camera);
  cameraRef.current = camera;
  const movedRef = useRef(moved);
  movedRef.current = moved;

  // Measure actual stage dimensions with ResizeObserver and keep untouched/preview cameras fitted.
  useLayoutEffect(() => {
    const el = stageRef.current;
    const fallbackH = still ? DEFAULT_PREVIEW_H : DEFAULT_STAGE_H;
    const readDim = () => ({
      w: el && el.clientWidth > 0 ? el.clientWidth : DEFAULT_STAGE_W,
      h: el && el.clientHeight > 0 ? el.clientHeight : fallbackH,
    });
    const sync = () => {
      const next = readDim();
      setStageSize((prev) => (prev.w === next.w && prev.h === next.h ? prev : next));
      if (!frame) return;
      if (still || !movedRef.current) {
        const fitted = fitCamera(frame, next.w, next.h, still ? 24 : 44, still ? activeRouteId : undefined);
        cameraRef.current = fitted;
        setCamera(fitted);
      }
    };
    sync();
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(sync);
    ro.observe(el);
    return () => ro.disconnect();
  }, [frame, still, full, activeRouteId]);

  const applyCamera = useCallback(
    (nextCam: Camera, nextMoved: boolean) => {
      cameraRef.current = nextCam;
      movedRef.current = nextMoved;
      setCamera(nextCam);
      setMoved(nextMoved);
      if (!still) {
        const s = readStore();
        s.camera = nextCam;
        s.moved = nextMoved;
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps -- scoped/keyed tracked via storeFor
    [still, storeKey],
  );

  const clampCenter = useCallback(
    (cx: number, cy: number, scale: number): { cx: number; cy: number } => {
      if (!frame) return { cx, cy };
      const padM = 80 / Math.max(scale, 1e-6);
      const halfSpanW = stageSize.w / (2 * Math.max(scale, 1e-6));
      const halfSpanH = stageSize.h / (2 * Math.max(scale, 1e-6));
      const { minX, maxX, minY, maxY } = frame.worldExtent;
      const slackX = Math.max(padM, halfSpanW);
      const slackY = Math.max(padM, halfSpanH);
      return {
        cx: clamp(cx, minX - slackX, maxX + slackX),
        cy: clamp(cy, minY - slackY, maxY + slackY),
      };
    },
    [frame, stageSize.w, stageSize.h],
  );

  const baseFit = useMemo(
    () => (frame ? fitCamera(frame, stageSize.w, stageSize.h, still ? 24 : 44, still ? activeRouteId : undefined) : { cx: 0, cy: 0, scale: 1 }),
    [frame, stageSize.w, stageSize.h, still, activeRouteId],
  );
  const minScale = Math.max(1e-7, baseFit.scale * 0.25);
  const maxScale = Math.max(0.5, baseFit.scale * 32);

  const panBy = useCallback(
    (dxPx: number, dyPx: number) => {
      const cur = cameraRef.current;
      const clamped = clampCenter(cur.cx - dxPx / cur.scale, cur.cy - dyPx / cur.scale, cur.scale);
      applyCamera({ ...clamped, scale: cur.scale }, true);
    },
    [clampCenter, applyCamera],
  );

  const zoomAtPoint = useCallback(
    (factor: number, sx = stageSize.w / 2, sy = stageSize.h / 2) => {
      const cur = cameraRef.current;
      const nextScale = clamp(cur.scale * factor, minScale, maxScale);
      if (nextScale === cur.scale) return;
      const wx = cur.cx + (sx - stageSize.w / 2) / cur.scale;
      const wy = cur.cy + (sy - stageSize.h / 2) / cur.scale;
      const clamped = clampCenter(wx - (sx - stageSize.w / 2) / nextScale, wy - (sy - stageSize.h / 2) / nextScale, nextScale);
      applyCamera({ ...clamped, scale: nextScale }, true);
    },
    [stageSize.w, stageSize.h, minScale, maxScale, clampCenter, applyCamera],
  );

  const resetCamera = useCallback(() => {
    if (!frame) return;
    setSelectedMarkerId(undefined);
    if (!still) readStore().selectedMarkerId = undefined;
    const fitted = fitCamera(frame, stageSize.w, stageSize.h, 44, undefined);
    applyCamera(fitted, false);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- scoped/keyed tracked via storeFor
  }, [frame, stageSize.w, stageSize.h, applyCamera, still, storeKey]);

  const selectRoute = useCallback(
    (id: string) => {
      setSelectedRouteId(id);
      if (!still) readStore().selectedRouteId = id;
      if (frame) {
        const fitted = fitCamera(frame, stageSize.w, stageSize.h, 44, id);
        applyCamera(fitted, false);
      }
      // eslint-disable-next-line react-hooks/exhaustive-deps -- scoped/keyed tracked via storeFor
    },
    [frame, stageSize.w, stageSize.h, applyCamera, still, storeKey],
  );

  const focusMarker = useCallback(
    (m: UIMapMarker) => {
      setSelectedMarkerId(m.id);
      if (!still) readStore().selectedMarkerId = m.id;
      if (!frame || still) return;
      const p = frame.project({ lat: m.lat, lon: m.lon });
      const targetScale = Math.max(cameraRef.current.scale, baseFit.scale * 1.35);
      applyCamera({ cx: p.x, cy: p.y, scale: clamp(targetScale, minScale, maxScale) }, true);
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps -- scoped/keyed tracked via storeFor
    [frame, still, baseFit.scale, minScale, maxScale, applyCamera, storeKey],
  );

  const triggerAction = useCallback(
    (action: UIAction) => {
      if ((busy && busy !== action.id) || (canPress && !canPress(action))) return;
      if (full && action.push) setFull(false);
      onPress(action);
    },
    [busy, canPress, full, onPress],
  );

  const handleMarkerClick = useCallback(
    (m: UIMapMarker) => {
      if (still) return;
      if (m.action && !m.detail) {
        setSelectedMarkerId(m.id);
        if (!still) readStore().selectedMarkerId = m.id;
        triggerAction(m.action);
        return;
      }
      focusMarker(m);
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps -- scoped/keyed tracked via storeFor
    [still, triggerAction, focusMarker, storeKey],
  );

  // Pointer drag, pinch zoom, and mouse wheel on the interactive stage.
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  useLayoutEffect(() => {
    const area = stageRef.current;
    if (!area || still) return;
    pointers.current.clear();
    let swallow: { el: Element; fn: (ev: Event) => void } | null = null;
    const dropSwallow = () => {
      if (!swallow) return;
      swallow.el.removeEventListener("click", swallow.fn, true);
      swallow = null;
    };
    const stopClick = (ev: Event) => ev.stopPropagation();

    const localPt = (e: { clientX: number; clientY: number }) => {
      const r = area.getBoundingClientRect();
      return { x: e.clientX - r.left, y: e.clientY - r.top };
    };
    const gestureOf = (ps: Map<number, { x: number; y: number }>) => {
      const [a, b] = ps.values();
      if (!a) return undefined;
      if (!b) return { cx: a.x, cy: a.y, d: 0 };
      return { cx: (a.x + b.x) / 2, cy: (a.y + b.y) / 2, d: Math.hypot(a.x - b.x, a.y - b.y) };
    };

    const onDown = (e: PointerEvent) => {
      if (e.button !== 0 || pointers.current.size >= 2) return;
      const onBtn = (e.target as Element | null)?.closest("[data-map-toolbar]");
      if (onBtn) return;
      const ctrl = (e.target as Element | null)?.closest("button, a");
      if (swallow && ctrl === swallow.el) dropSwallow();
      pointers.current.set(e.pointerId, localPt(e));
    };

    const onMove = (e: PointerEvent) => {
      const prev = pointers.current.get(e.pointerId);
      if (!prev) return;
      const next = localPt(e);
      const ctrl = (e.target as Element | null)?.closest("button, a") ?? null;
      const captured = area.hasPointerCapture?.(e.pointerId);
      if (!captured && ctrl && Math.hypot(next.x - prev.x, next.y - prev.y) < 4) return;
      if (!captured) {
        area.setPointerCapture?.(e.pointerId);
        if (ctrl) {
          dropSwallow();
          ctrl.addEventListener("click", stopClick, { capture: true, once: true });
          swallow = { el: ctrl, fn: stopClick };
        }
      }
      const g0 = gestureOf(pointers.current)!;
      pointers.current.set(e.pointerId, next);
      const g1 = gestureOf(pointers.current)!;
      if (g0.d > 0 && g1.d > 0) zoomAtPoint(g1.d / g0.d, g0.cx, g0.cy);
      panBy(g1.cx - g0.cx, g1.cy - g0.cy);
    };

    const onEnd = (e: PointerEvent) => {
      pointers.current.delete(e.pointerId);
      if (swallow) setTimeout(dropSwallow, 0);
    };

    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const unit = e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? area.clientHeight : 1;
      const pt = localPt(e);
      if (e.ctrlKey || e.metaKey || Math.abs(e.deltaY) >= Math.abs(e.deltaX)) {
        zoomAtPoint(Math.exp(clamp(-e.deltaY * unit * 0.004, -0.6, 0.6)), pt.x, pt.y);
      } else {
        panBy(-e.deltaX * unit, -e.deltaY * unit);
      }
    };

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || e.target !== area) return;
      if (e.key === "ArrowLeft") (e.preventDefault(), panBy(KEY_PAN_PX, 0));
      else if (e.key === "ArrowRight") (e.preventDefault(), panBy(-KEY_PAN_PX, 0));
      else if (e.key === "ArrowUp") (e.preventDefault(), panBy(0, KEY_PAN_PX));
      else if (e.key === "ArrowDown") (e.preventDefault(), panBy(0, -KEY_PAN_PX));
      else if (e.key === "+" || e.key === "=") (e.preventDefault(), zoomAtPoint(STEP));
      else if (e.key === "-" || e.key === "_") (e.preventDefault(), zoomAtPoint(1 / STEP));
      else if (e.key === "0") (e.preventDefault(), resetCamera());
    };

    area.addEventListener("wheel", onWheel, { passive: false });
    area.addEventListener("pointerdown", onDown);
    document.addEventListener("pointermove", onMove);
    document.addEventListener("pointerup", onEnd);
    document.addEventListener("pointercancel", onEnd);
    area.addEventListener("lostpointercapture", onEnd);
    area.addEventListener("keydown", onKeyDown);
    return () => {
      dropSwallow();
      area.removeEventListener("wheel", onWheel);
      area.removeEventListener("pointerdown", onDown);
      document.removeEventListener("pointermove", onMove);
      document.removeEventListener("pointerup", onEnd);
      document.removeEventListener("pointercancel", onEnd);
      area.removeEventListener("lostpointercapture", onEnd);
      area.removeEventListener("keydown", onKeyDown);
    };
  }, [still, full, panBy, zoomAtPoint, resetCamera]);

  if (!frame || !parsed) {
    return (
      <div className="px-4 py-3">
        {item.title && <div className="mb-2 truncate text-meta font-medium text-ink">{item.title}</div>}
        <div
          role="img"
          aria-label={altText}
          data-map-fallback=""
          className="flex items-center gap-2.5 rounded-card border border-line bg-inset px-3.5 py-3 text-meta text-ink-2"
        >
          <Icon name="map" size={16} className="shrink-0 text-ink-3" />
          <span className="min-w-0 flex-1 break-words">{altText}</span>
        </div>
        {item.caption && <p className="mt-2 text-submeta text-ink-3">{item.caption}</p>}
      </div>
    );
  }

  const toScreen = (c: UIMapCoordinate) => {
    const p = frame.project(c);
    return {
      x: stageSize.w / 2 + (p.x - camera.cx) * camera.scale,
      y: stageSize.h / 2 + (p.y - camera.cy) * camera.scale,
    };
  };

  // Vector graticule lines for geographic orientation without network tiles.
  const worldW = stageSize.w / camera.scale;
  const worldH = stageSize.h / camera.scale;
  const xMin = camera.cx - worldW / 2;
  const xMax = camera.cx + worldW / 2;
  const yMin = camera.cy - worldH / 2;
  const yMax = camera.cy + worldH / 2;
  const stepM = gridStepMeters(Math.max(worldW, worldH));
  const vGrid: number[] = [];
  for (let gx = Math.ceil(xMin / stepM) * stepM; gx <= xMax && vGrid.length < 16; gx += stepM) {
    vGrid.push(stageSize.w / 2 + (gx - camera.cx) * camera.scale);
  }
  const hGrid: number[] = [];
  for (let gy = Math.ceil(yMin / stepM) * stepM; gy <= yMax && hGrid.length < 16; gy += stepM) {
    hGrid.push(stageSize.h / 2 + (gy - camera.cy) * camera.scale);
  }

  const orderedRoutes = [
    ...routes.filter((r) => activeRouteId !== undefined && r.id !== activeRouteId).map((r) => ({ route: r, selected: false })),
    ...routes.filter((r) => activeRouteId === undefined || r.id === activeRouteId).map((r) => ({ route: r, selected: true })),
  ];

  const centerGeo = frame.unproject(camera.cx, camera.cy);
  const coordReadout = `${Math.abs(centerGeo.lat).toFixed(3)}°${centerGeo.lat >= 0 ? "N" : "S"} · ${Math.abs(centerGeo.lon).toFixed(3)}°${centerGeo.lon >= 0 ? "E" : "W"}`;
  const scaleInfo = niceScaleBar(camera.scale);

  const svgOverlay = (
    <>
      <svg
        aria-hidden
        data-map-svg=""
        className="pointer-events-none absolute inset-0 size-full"
        viewBox={`0 0 ${stageSize.w} ${stageSize.h}`}
        preserveAspectRatio="none"
      >
        {vGrid.map((sx, i) => (
          <line key={`vx${i}`} x1={sx} y1={0} x2={sx} y2={stageSize.h} stroke="var(--line)" strokeWidth={1} strokeOpacity={0.75} />
        ))}
        {hGrid.map((sy, i) => (
          <line key={`hy${i}`} x1={0} y1={sy} x2={stageSize.w} y2={sy} stroke="var(--line)" strokeWidth={1} strokeOpacity={0.75} />
        ))}
        {orderedRoutes.map(({ route, selected }) => {
          const pts = route.points.map((pt) => {
            const p = toScreen(pt);
            return `${p.x.toFixed(1)},${p.y.toFixed(1)}`;
          }).join(" ");
          const color = resolveColor(route.tone, route.color);
          const strokeW = selected ? (still ? 3.5 : 4.5) : (still ? 2 : 3);
          return (
            <g key={route.id}>
              {selected && (
                <polyline
                  points={pts}
                  fill="none"
                  stroke="var(--surface)"
                  strokeWidth={strokeW + 3}
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeOpacity={0.85}
                />
              )}
              <polyline
                data-map-route-line={route.id}
                data-selected={selected ? "true" : undefined}
                points={pts}
                fill="none"
                stroke={color}
                strokeWidth={strokeW}
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeOpacity={selected ? 1 : 0.42}
              />
            </g>
          );
        })}
      </svg>

      {markers.map((m) => {
        const pos = toScreen({ lat: m.lat, lon: m.lon });
        const selected = activeMarkerId === m.id;
        const style = { left: `${pos.x.toFixed(1)}px`, top: `${pos.y.toFixed(1)}px`, transform: "translate(-50%, -50%)" };
        const pinContent = (
          <span className="flex flex-col items-center gap-0.5">
            <MarkerPinBadge marker={m} selected={selected} />
            {m.label && (
              <span className={cx(
                "max-w-[140px] truncate rounded-chip border border-line bg-surface/95 px-1.5 py-0.5 text-label font-medium text-ink shadow-hairline",
                selected && "border-accent",
              )}>
                {m.label}
              </span>
            )}
          </span>
        );
        if (still) {
          return (
            <div key={m.id} aria-hidden data-map-marker={m.id} className="pointer-events-none absolute z-[1]" style={style}>
              {pinContent}
            </div>
          );
        }
        const aria = [m.label, m.subtitle, m.detail].filter(Boolean).join(" · ");
        return (
          <button
            key={m.id}
            type="button"
            data-map-marker={m.id}
            aria-label={aria || m.label}
            aria-current={selected ? "true" : undefined}
            title={m.label}
            disabled={Boolean(m.action && !m.detail && ((busy && busy !== m.action.id) || (canPress && !canPress(m.action))))}
            onClick={() => handleMarkerClick(m)}
            className="touch-hit absolute z-[2] flex min-h-8 min-w-8 cursor-pointer items-center justify-center rounded-full focus-visible:outline-2 focus-visible:outline-accent"
            style={style}
          >
            {pinContent}
          </button>
        );
      })}

      {/* Metric scale bar & coordinates */}
      <div
        aria-hidden
        data-map-scale=""
        className="pointer-events-none absolute bottom-2.5 left-3 z-[2] flex items-center gap-2 rounded-chip border border-line bg-surface/90 px-2 py-1 text-submeta text-ink-3 shadow-hairline"
      >
        <span className="flex flex-col items-center gap-0.5">
          <span className="tabular-nums leading-none">{scaleInfo.label}</span>
          <span className="h-1 border-x border-b border-ink-2" style={{ width: scaleInfo.px }} />
        </span>
        {!still && <span className="hidden border-l border-line pl-2 tabular-nums sm:inline">{coordReadout}</span>}
      </div>
    </>
  );

  const stageToolbar = !still && (
    <div
      data-map-toolbar=""
      className="absolute right-3 bottom-3 z-[3] flex items-center gap-0.5 rounded-full border border-line bg-surface/90 p-1 shadow-card backdrop-blur"
    >
      <IconButton icon="minus" label={t("extensions-viewbody.map_zoom_out")} size={32} onClick={() => zoomAtPoint(1 / STEP)} />
      <IconButton icon="plus" label={t("extensions-viewbody.map_zoom_in")} size={32} onClick={() => zoomAtPoint(STEP)} />
      <IconButton icon="retry" label={t("extensions-viewbody.map_reset")} size={32} disabled={!moved} onClick={resetCamera} />
      {!full && <IconButton icon="expand" label={t("extensions-viewbody.map_full_screen")} size={32} buttonRef={opener} onClick={() => setFull(true)} />}
    </div>
  );

  const interactiveStage = (
    <div
      ref={stageRef}
      role="group"
      aria-label={altText}
      tabIndex={0}
      data-map-stage=""
      className={cx(
        "relative w-full touch-none overflow-hidden overscroll-none select-none rounded-card border border-line bg-inset cursor-grab active:cursor-grabbing focus-visible:outline-2 focus-visible:outline-accent",
        full ? "min-h-[220px] flex-1" : "map-canvas",
      )}
    >
      {svgOverlay}
      {stageToolbar}
    </div>
  );

  const routeSelector = routes.length > 1 ? (
    <div
      role="tablist"
      aria-label={t("extensions-viewbody.map_routes")}
      className="flex items-center gap-2 overflow-x-auto pb-0.5"
    >
      {routes.map((r, i) => {
        const selected = activeRouteId === r.id;
        const title = routeTitle(r, i);
        const meta = routeSummaryMeta(r, title);
        return (
          <button
            key={r.id}
            type="button"
            role="tab"
            aria-selected={selected}
            data-map-route={r.id}
            onClick={() => selectRoute(r.id)}
            className={cx(
              "inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full border px-3 py-1.5 text-label font-medium transition-colors",
              selected
                ? "border-accent bg-accent-tint text-accent-ink"
                : "border-line bg-surface text-ink-2 hover:bg-hover hover:text-ink",
            )}
          >
            <ModeGlyph mode={r.mode} size={13} className="shrink-0" style={!selected ? { color: resolveColor(r.tone, r.color) } : undefined} />
            <span>{title}</span>
            {meta && <span className={cx("tabular-nums", selected ? "text-accent-ink" : "text-ink-3")}>{meta}</span>}
          </button>
        );
      })}
    </div>
  ) : activeRoute ? (
    (() => {
      const meta = routeSummaryMeta(activeRoute);
      const summary = activeRoute.summary?.trim();
      if (!meta && !summary) return null;
      return (
        <div data-map-route-summary="" className="flex items-center gap-2 text-meta text-ink-2">
          <ModeGlyph mode={activeRoute.mode} size={14} className="shrink-0" style={{ color: resolveColor(activeRoute.tone, activeRoute.color) }} />
          <div className="min-w-0 flex-1">
            {meta && <span className="font-medium text-ink tabular-nums">{meta}</span>}
            {meta && summary && <span className="mx-1.5 text-ink-3">·</span>}
            {summary && <span className="text-submeta text-ink-3">{summary}</span>}
          </div>
        </div>
      );
    })()
  ) : null;

  const markerDetailCard = activeMarker && (
    <div
      data-map-detail={activeMarker.id}
      className="flex items-start gap-2.5 rounded-control border border-line bg-inset px-3 py-2.5"
    >
      <div className="pt-0.5">
        <MarkerPinBadge marker={activeMarker} selected />
      </div>
      <div className="min-w-0 flex-1">
        <div className="truncate text-meta font-semibold text-ink">{activeMarker.label}</div>
        {activeMarker.subtitle && <div className="text-submeta text-ink-3">{activeMarker.subtitle}</div>}
        {activeMarker.detail && <p className="mt-1 text-label leading-relaxed text-ink-2 break-words">{activeMarker.detail}</p>}
      </div>
      {activeMarker.action && (
        <button
          type="button"
          data-map-open-stop={activeMarker.id}
          disabled={(!!busy && busy !== activeMarker.action.id) || !(canPress ? canPress(activeMarker.action) : true)}
          aria-busy={busy === activeMarker.action.id || undefined}
          onClick={() => triggerAction(activeMarker.action!)}
          className="inline-flex h-7 shrink-0 items-center gap-1 whitespace-nowrap rounded-control bg-ink px-2.5 text-label font-medium text-surface transition-opacity hover:opacity-90 disabled:opacity-45"
        >
          {busy === activeMarker.action.id ? <Spinner size={12} /> : <Icon name="chevronRight" size={12} />}
          <span>{t("extensions-viewbody.map_open_stop")}</span>
        </button>
      )}
      <IconButton
        icon="x"
        label={t("extensions-viewbody.map_close")}
        size={28}
        onClick={() => {
          setSelectedMarkerId(undefined);
          if (!still) readStore().selectedMarkerId = undefined;
        }}
      />
    </div>
  );

  const shouldShowList = !still && (parsed.show_list ?? (routes.length > 1 || markers.length > 1));
  const stopList = shouldShowList && markers.length > 0 && (
    <div data-map-stops="" aria-label={t("extensions-viewbody.map_stops")} className="flex flex-col gap-1.5">
      {markers.map((m) => {
        const selected = activeMarkerId === m.id;
        const canAct = Boolean(m.action && (!canPress || canPress(m.action)) && (!busy || busy === m.action.id));
        return (
          <button
            key={m.id}
            type="button"
            data-map-stop={m.id}
            aria-current={selected ? "true" : undefined}
            onClick={() => {
              if (m.action && !m.detail && canAct) {
                setSelectedMarkerId(m.id);
                if (!still) readStore().selectedMarkerId = m.id;
                triggerAction(m.action);
              } else {
                focusMarker(m);
              }
            }}
            className={cx(
              "flex w-full items-center gap-2.5 rounded-control border px-3 py-2 text-left transition-colors",
              selected ? "border-accent bg-accent-tint" : "border-line bg-inset hover:bg-hover",
            )}
          >
            <MarkerPinBadge marker={m} selected={selected} />
            <div className="min-w-0 flex-1">
              <div className="truncate text-meta font-medium text-ink">{m.label}</div>
              {(m.subtitle || m.detail) && (
                <div className="truncate text-submeta text-ink-3">{m.subtitle || m.detail}</div>
              )}
            </div>
            {m.action && <Icon name="chevronRight" size={13} className="shrink-0 text-ink-3" />}
          </button>
        );
      })}
    </div>
  );

  const linkRow = !still && links.length > 0 && (
    <div className="flex flex-wrap items-center gap-2">
      {links.map((l, i) => (
        <a
          key={`${l.title}\u0000${l.url}`}
          data-map-link={i}
          href={linkHref(l.url)}
          target="_blank"
          rel="noreferrer noopener"
          onClick={(e) => {
            if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey) return;
            e.preventDefault();
            if (full && opensInApp(l.url)) setFull(false);
            openLink(l.url, l.title, linkManager);
          }}
          className="inline-flex h-7 items-center gap-1.5 whitespace-nowrap rounded-full border border-line bg-surface px-3 text-label font-medium text-accent-ink transition-colors hover:bg-hover"
        >
          <span className="truncate">{l.title}</span>
          <Icon name="external" size={11} className="shrink-0" />
        </a>
      ))}
    </div>
  );

  if (full) {
    return (
      <>
        <div aria-hidden className="px-4 py-3">
          <div className={compact ? "map-preview" : "map-canvas"} />
        </div>
        {createPortal(
          <div
            ref={dialogRef}
            role="dialog"
            aria-modal="true"
            aria-label={item.title || altText}
            tabIndex={-1}
            className="fixed inset-0 z-50 flex flex-col gap-3 p-3 outline-none sm:p-4"
            style={{ background: "var(--page)" }}
          >
            <div className="flex shrink-0 items-center gap-2">
              <div className="min-w-0 flex-1 truncate text-card-title font-semibold text-ink">
                {item.title || t("extensions-viewbody.map")}
              </div>
              <IconButton icon="x" label={t("extensions-viewbody.map_close")} size={36} onClick={() => setFull(false)} />
            </div>
            {interactiveStage}
            {(routes.length > 0 || activeMarker || shouldShowList || links.length > 0 || parsed.caption) && (
              <div className="flex max-h-[38dvh] shrink-0 flex-col gap-2.5 overflow-y-auto rounded-card border border-line bg-surface p-3 shadow-card">
                {routeSelector}
                {markerDetailCard}
                {stopList}
                {linkRow}
                {parsed.caption && <p className="text-submeta text-ink-3">{parsed.caption}</p>}
              </div>
            )}
          </div>,
          document.body,
        )}
      </>
    );
  }

  if (compact) {
    const previewBody = (
      <div ref={stageRef} className="relative size-full overflow-hidden">
        {svgOverlay}
        {!isStatic && (
          <span
            aria-hidden
            className="pointer-events-none absolute right-2.5 bottom-2.5 z-[2] flex size-7 items-center justify-center rounded-full border border-line bg-surface/90 text-ink-2 shadow-card"
          >
            <Icon name="expand" size={13} />
          </span>
        )}
      </div>
    );
    return (
      <div className="flex flex-col gap-2 px-4 py-3">
        {item.title && <div className="truncate text-meta font-medium text-ink">{item.title}</div>}
        {isStatic ? (
          <div
            role="img"
            aria-label={altText}
            data-map-preview=""
            className="map-preview relative w-full overflow-hidden rounded-card border border-line bg-inset"
          >
            {previewBody}
          </div>
        ) : (
          <button
            type="button"
            ref={opener}
            data-map-preview=""
            aria-label={t("extensions-viewbody.map_full_screen")}
            title={altText}
            onClick={() => setFull(true)}
            className="map-preview group block w-full cursor-zoom-in overflow-hidden rounded-card border border-line bg-inset text-left focus-visible:outline-2 focus-visible:outline-accent"
          >
            {previewBody}
          </button>
        )}
        {activeRoute && (() => {
          const meta = routeSummaryMeta(activeRoute);
          const summary = activeRoute.summary?.trim();
          if (!meta && !summary) return null;
          return (
            <div data-map-route-summary="" className="flex items-center gap-2 text-submeta text-ink-2">
              <ModeGlyph mode={activeRoute.mode} size={13} className="shrink-0" style={{ color: resolveColor(activeRoute.tone, activeRoute.color) }} />
              <span className="truncate font-medium text-ink tabular-nums">{meta || activeRoute.label}</span>
              {summary && <span className="min-w-0 truncate text-ink-3">{summary}</span>}
            </div>
          );
        })()}
        {parsed.caption && <p className="text-submeta text-ink-3">{parsed.caption}</p>}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-3 px-4 py-3">
      {item.title && <h4 className="truncate text-card-title font-semibold text-ink">{item.title}</h4>}
      {interactiveStage}
      {routeSelector}
      {markerDetailCard}
      {stopList}
      {linkRow}
      {parsed.caption && <p className="text-submeta text-ink-3">{parsed.caption}</p>}
    </div>
  );
}
