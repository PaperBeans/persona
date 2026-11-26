// Existing Health data stays in the default panel when the optional Health module is absent or off (#13430).
// This file owns only that read-only routing rule; it never imports the module. The enabled module still claims
// /health and /tracks/health through the module host before this fallback is consulted.
import { moduleOn } from "../core/module-host";
import type { Route } from "./router";
import type { Track } from "./tracks";

export const HEALTH_TRACK = "health";

/** The built-in Health track is older than the optional tracks API. Its archive must not turn into "space not found"
 *  on a hub that answers 404 there. The fallback stays in the lazily loaded page, not the first-screen sidebar. */
export function healthArchiveTrack(): Track {
  return {
    slug: HEALTH_TRACK,
    kind: "health",
    title: "Health",
    icon: "heart",
    description: "",
    default_categories: ["health"],
    template: "health",
    builtin: true,
    archived: false,
    revision: 0,
    config: {
      schema: 1,
      template: "health",
      context: "track",
      default_categories: ["health"],
      tag_focus: [],
      rhythms: [],
      attention: { push_cap: "P0", deep_default: "quick", enter_mode: "none" },
      supervision: "none",
      usage: [],
      assembly: "track",
      capture_target: true,
      channels: [],
      sidebar: { group: "spaces" },
    },
  };
}

/** Whether the built-in Health space must use its read-only default-panel view. No registered module (a core-only
 *  build) is the same as switched off: stored conversations, reports and memory still need a way to be read. */
export function healthReadOnly(slug: string): boolean {
  return slug === HEALTH_TRACK && !moduleOn(HEALTH_TRACK);
}

function conversationRoute(path: readonly string[]): Route {
  const fresh = !path.length || (path[0] === "new" && !path[1]);
  return { name: "track", slug: HEALTH_TRACK, ...(fresh ? { fresh: true } : { sid: path[0] }) };
}

/** The read-only route for an old Health address, or null when the enabled module owns it (or it is not Health).
 *  Both the module's base and its /tracks/health alias are accepted so a core-only build keeps the same bookmarks. */
export function healthReadOnlyRoute(segments: readonly string[]): Route | null {
  if (!healthReadOnly(HEALTH_TRACK)) return null;
  if (segments[0] === HEALTH_TRACK) return conversationRoute(segments.slice(1));
  if (segments[0] === "tracks" && segments[1] === HEALTH_TRACK) return conversationRoute(segments.slice(2));
  return null;
}
