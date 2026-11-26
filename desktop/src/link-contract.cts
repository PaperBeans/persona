export interface LinkCommand {
  readonly action: "status" | "pair-active" | "pair-passive" | "reconnect" | "node-install" | "node-status" | "rename" | "plugin-install" | "plugin-uninstall";
  readonly value?: string;
}

/** Extract a clean `persona://link?...` or `jarvis://link?...` URI from raw input, quoted text, or a CLI command. */
export function extractLinkPairURI(raw: string): string | undefined {
  let trimmed = raw.trim();
  if (trimmed.length >= 2) {
    const first = trimmed[0];
    const last = trimmed[trimmed.length - 1];
    if ((first === "'" && last === "'") || (first === '"' && last === '"')) {
      trimmed = trimmed.slice(1, -1).trim();
    }
  }
  const candidate = /^(?:persona|jarvis):\/\/link\?/i.test(trimmed) && !/[\s\u0000-\u001f\u007f]/.test(trimmed)
    ? trimmed
    : trimmed.match(/(?:persona|jarvis):\/\/link\?[^\s'"<>]+/i)?.[0];
  if (!candidate) return undefined;
  try {
    const parsed = new URL(candidate);
    return (parsed.protocol === "persona:" || parsed.protocol === "jarvis:") && parsed.host === "link" && parsed.search
      ? parsed.href
      : undefined;
  } catch {
    return undefined;
  }
}

export function linkArguments(command: LinkCommand): { args: string[]; stdin?: string } | undefined {
  const value = command.value?.trim() ?? "";
  switch (command.action) {
    case "status": return { args: ["status", "--json"] };
    case "node-status": return { args: ["node", "status", "--json"] };
    case "reconnect": return { args: ["reconnect"] };
    case "node-install": return { args: ["node", "install"] };
    case "plugin-install": return { args: ["plugin", "install"] };
    case "plugin-uninstall": return { args: ["plugin", "uninstall"] };
    case "rename": return value.length > 0 && value.length <= 40 ? { args: ["name", value] } : undefined;
    case "pair-active": {
      const linkURI = extractLinkPairURI(value);
      return linkURI ? { args: ["pair", "-"], stdin: linkURI } : undefined;
    }
    case "pair-passive": return !value || /^:\d{1,5}$/.test(value) ? { args: ["pair", "--listen", ...(value ? [value] : [])] } : undefined;
  }
}

/** The CLI output is an allowlisted status view, not a dump of link.json, logs or arbitrary local paths. */
export function publicLinkStatus(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  const v = value as Record<string, unknown>;
  const out: Record<string, unknown> = {};
  for (const key of ["running", "paired", "mode", "machine", "machine_name", "hub_name", "trust", "error"]) {
    const item = v[key];
    if (typeof item === "boolean" || (typeof item === "string" && item.length <= 200)) out[key] = item;
  }
  if (v.link && typeof v.link === "object" && !Array.isArray(v.link)) {
    const live = v.link as Record<string, unknown>;
    out.link = {
      ...(typeof live.state === "string" && live.state.length <= 32 ? { state: live.state } : {}),
      ...(live.pending === true ? { pending: true } : {}),
    };
  }
  if (v.claude_plugin && typeof v.claude_plugin === "object" && !Array.isArray(v.claude_plugin)) {
    // The Claude Code plugin's state words only: never its config directories (paths) or errors.
    const plugin = v.claude_plugin as Record<string, unknown>;
    out.claude_plugin = {
      ...(typeof plugin.state === "string" && plugin.state.length <= 32 ? { state: plugin.state } : {}),
      ...(typeof plugin.auto === "boolean" ? { auto: plugin.auto } : {}),
      ...(typeof plugin.version === "string" && plugin.version.length <= 64 ? { version: plugin.version } : {}),
    };
  }
  return out;
}
