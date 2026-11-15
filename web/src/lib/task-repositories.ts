import type { Todo } from "./todos";
import { api, credentialOwner, getToken, pastHub } from "./api";
import { MANAGER_HEADER } from "./manager-choice";
import type { RepositoryIdentity } from "./repository-identity";

export type RepositoryBinding = RepositoryIdentity & {
  id: string; name?: string; aliases?: string[]; purpose?: string; verified_at: string; clone_url?: string;
};
export type RequestedRef = { kind: "default"; value?: never } | { kind: "branch" | "tag" | "commit" | "ref"; value: string };
export type RepositoryLayout = "root" | "container";
export type RepositorySourceSnapshot = {
  identity: RepositoryIdentity; clone_url: string; transport_origin: "catalog" | "canonical_default"; catalog_revision: number;
};
export type RepositoryLinkInput = { url: string; kind?: never; number?: never; title?: string } | { kind: "issue" | "pull_request"; number: string; url?: never; title?: string };
export type TaskRepositoryLink = { kind: "issue" | "pull_request"; number: string; canonical_url: string; title?: string };
export type TaskRepositoryInput = { repo_id: string; directory_alias?: string; requested_ref?: RequestedRef; links?: RepositoryLinkInput[] };
export type TaskRepository = Omit<TaskRepositoryInput, "links" | "requested_ref"> & {
  requested_ref: RequestedRef; source_snapshot: RepositorySourceSnapshot; links: TaskRepositoryLink[];
  /** `unlisted`: the Agent linked a repository that is not in the saved catalog (its source is kept). */
  catalog_state?: "current" | "removed" | "identity_changed" | "unlisted";
};
export type TaskRepositorySet = { schema_version: 1; manager: string; todo_id: string; revision: number; repositories: TaskRepository[] };
export type RepositoryCatalog = { manager: string; catalog_revision: number; repositories: RepositoryBinding[] };
export type CreateRepositoryBindings = { catalog_revision: number; repositories: TaskRepositoryInput[] };
export type TaskRepositoryPreparationSnapshot = {
  schema_version: 1; manager: string; todo_id: string; revision: number; digest: string;
  repositories: Array<Pick<TaskRepository, "repo_id" | "directory_alias" | "requested_ref" | "source_snapshot">>;
};

/** The same conservative directory-name rule as repository.ValidateAlias in Go. Empty is no override. */
export function validRepositoryAlias(alias: string): boolean {
  if (!alias) return true;
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(alias) || alias.endsWith(".")) return false;
  return !/^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)/i.test(alias);
}
export function repositoryRelativePath(layout: RepositoryLayout, alias: string, count: number): string | undefined {
  if (layout === "root" && count === 1) return ".";
  return layout === "container" && !!alias && validRepositoryAlias(alias) ? alias : undefined;
}
const MAX_LINK_TITLE_BYTES = 1024;
/**
 * The server trims a source title and refuses controls or more than 1,024 UTF-8 bytes. Reading and writing with the
 * same rule keeps a saved title displayable and a draft saveable; an absent or empty title stays absent, never guessed.
 */
export function repositoryLinkTitle(value: unknown): string | undefined {
  if (typeof value !== "string") return undefined;
  const title = value.trim();
  if (!title || /\p{Cc}/u.test(title) || new TextEncoder().encode(title).length > MAX_LINK_TITLE_BYTES) return undefined;
  return title;
}
export function repositoryDraft(rows: TaskRepository[]): TaskRepositoryInput[] {
  return rows.map((row) => ({ repo_id: row.repo_id, directory_alias: row.directory_alias, requested_ref: { ...row.requested_ref },
    links: row.links.map(({ kind, number, title }) => {
      const kept = repositoryLinkTitle(title);
      return { kind, number, ...(kept ? { title: kept } : {}) };
    }) }));
}

/** One issue or pull request of a bound repository, ready to display. */
export type RepositoryLinkView = { kind: "issue" | "pull_request"; number: string; url: string; label: string; title?: string };
const LINK_SEGMENTS = {
  github: { issue: "issues", pull_request: "pull" },
  gitlab: { issue: "-/issues", pull_request: "-/merge_requests" },
} as const;
const linkURL = (identity: RepositoryIdentity, kind: "issue" | "pull_request", number: string) =>
  `${identity.canonical_url}/${LINK_SEGMENTS[identity.provider as keyof typeof LINK_SEGMENTS][kind]}/${number}`;

/**
 * Reads an issue/PR link of a repository from either form the store accepts: a pasted URL (held to the saved identity
 * exactly as `repository.NormalizeLink` does) or a saved kind + number. Null when it does not belong to this repository.
 */
export function repositoryLinkView(identity: RepositoryIdentity, link: TaskRepositoryLink | RepositoryLinkInput): RepositoryLinkView | null {
  const segments = LINK_SEGMENTS[identity.provider as keyof typeof LINK_SEGMENTS];
  if (!segments) return null;
  let kind: string | undefined, number: string | undefined;
  if ("url" in link && link.url !== undefined) {
    const raw = link.url.trim();
    if (!raw || raw.length > 2048) return null;
    let url: URL, base: URL;
    try { url = new URL(raw); base = new URL(identity.canonical_url); } catch { return null; }
    if (url.protocol !== base.protocol || !["http:", "https:"].includes(url.protocol) || url.username || url.password
      || url.host !== base.host || url.search || url.hash) return null;
    const prefix = base.pathname.replace(/\/$/, "") + "/";
    for (const [candidate, segment] of Object.entries(segments)) {
      if (!url.pathname.startsWith(prefix + segment + "/")) continue;
      // The server reads the number to the end of the path, so a trailing slash or a deeper path is not this link.
      const rest = url.pathname.slice(prefix.length + segment.length + 1);
      if (!rest || rest.includes("/")) return null;
      kind = candidate; number = rest;
      break;
    }
  } else {
    kind = link.kind; number = link.number;
  }
  if ((kind !== "issue" && kind !== "pull_request") || !number || !/^[1-9][0-9]{0,19}$/.test(number)) return null;
  const url = linkURL(identity, kind, number);
  // A saved link's canonical URL must still name this repository and this number. A title belongs to that exact link,
  // never to another repository or number that happens to be in the same response.
  if ("canonical_url" in link && link.canonical_url !== url) return null;
  const label = kind === "issue" ? `#${number}` : `${identity.provider === "gitlab" ? "MR !" : "PR #"}${number}`;
  const title = repositoryLinkTitle(link.title);
  return { kind, number, url, label, ...(title ? { title } : {}) };
}
/** The same canonical-source check for a saved link, used before trusting a response's kind, number, URL or title. */
export function validTaskRepositoryLink(identity: RepositoryIdentity, link: TaskRepositoryLink): boolean {
  const segments = LINK_SEGMENTS[identity.provider as keyof typeof LINK_SEGMENTS];
  if (!segments || !link || !["issue", "pull_request"].includes(link.kind) || typeof link.number !== "string"
    || !/^[1-9][0-9]{0,19}$/.test(link.number)) return false;
  if (link.canonical_url !== linkURL(identity, link.kind, link.number)) return false;
  // An empty title is the older servers' "none"; a nonempty one must be exactly what the server accepted (trimmed).
  return link.title === undefined || link.title === "" || repositoryLinkTitle(link.title) === link.title;
}
const revision = (n: unknown): n is number => Number.isSafeInteger(n) && (n as number) >= 0;
// Check the server-owned envelope; repository normalization remains in the shared parser.
function validIdentity(identity: RepositoryIdentity): boolean {
  if (!identity || !["github", "gitlab"].includes(identity.provider) || typeof identity.instance_url !== "string"
    || typeof identity.path !== "string" || !identity.path || identity.canonical_url !== `${identity.instance_url}/${identity.path}`) return false;
  try {
    const instance = new URL(identity.instance_url);
    return ["https:", "http:"].includes(instance.protocol) && instance.origin === identity.instance_url;
  } catch { return false; }
}
export function validRequestedRef(ref: RequestedRef): boolean {
  if (!ref || typeof ref.kind !== "string") return false;
  if (ref.kind === "default") return ref.value === undefined;
  return ["branch", "tag", "commit", "ref"].includes(ref.kind) && typeof ref.value === "string" && !!ref.value;
}
export function validRepositorySet(set: TaskRepositorySet, manager: string, todoId: string): boolean {
  return !!set && set.schema_version === 1 && set.manager === manager && set.todo_id === todoId && revision(set.revision)
    && Array.isArray(set.repositories) && set.repositories.length <= 20 && set.repositories.every((row) =>
      row && typeof row.repo_id === "string" && (row.directory_alias === undefined || typeof row.directory_alias === "string" && validRepositoryAlias(row.directory_alias))
      && validRequestedRef(row.requested_ref) && row.source_snapshot && validIdentity(row.source_snapshot.identity)
      && typeof row.source_snapshot.clone_url === "string" && revision(row.source_snapshot.catalog_revision)
      && ["catalog", "canonical_default"].includes(row.source_snapshot.transport_origin)
      && Array.isArray(row.links) && row.links.every((link) => validTaskRepositoryLink(row.source_snapshot.identity, link)));
}
export type RepositoryRequestContext = { manager: string; owner: string; token: string; isCurrent: () => boolean };
export function repositoryRequestContext(manager: string, owner: string, active: () => boolean): RepositoryRequestContext {
  return { manager, owner, token: getToken(), isCurrent: () => active() && credentialOwner() === owner };
}
export function repositoryRequestOptions(context: RepositoryRequestContext) {
  if (!context.isCurrent()) throw new Error("The editor connection changed");
  return { token: context.token, tokenOwner: context.owner, isAuthContextCurrent: context.isCurrent, headers: { [MANAGER_HEADER]: context.manager } };
}
export async function loadRepositoryCatalog(context: RepositoryRequestContext): Promise<RepositoryCatalog> {
  const result = await api<RepositoryCatalog>("/assistant/repositories", repositoryRequestOptions(context));
  if (!context.isCurrent() || result.manager !== context.manager || !revision(result.catalog_revision)
    || !Array.isArray(result.repositories) || !result.repositories.every((r) => typeof r.id === "string" && validIdentity(r))) throw new Error("Invalid repository catalog response");
  return result;
}
export async function loadTaskRepositories(context: RepositoryRequestContext, todoId: string): Promise<TaskRepositorySet> {
  const result = await api<TaskRepositorySet>(`/todos/${encodeURIComponent(todoId)}/repositories`, repositoryRequestOptions(context));
  if (!context.isCurrent() || !validRepositorySet(result, context.manager, todoId)) throw new Error("Invalid repository association response");
  return result;
}
export async function saveTaskRepositories(context: RepositoryRequestContext, todoId: string, revision: number, catalogRevision: number, rows: TaskRepositoryInput[]): Promise<TaskRepositorySet> {
  const result = await api<TaskRepositorySet>(`/todos/${encodeURIComponent(todoId)}/repositories`, {
    ...repositoryRequestOptions(context), method: "PUT", timeout: pastHub(8_000),
    json: { expected_revision: revision, expected_catalog_revision: catalogRevision, repositories: rows },
  });
  if (!context.isCurrent() || !validRepositorySet(result, context.manager, todoId) || result.revision !== revision + 1) throw new Error("Invalid repository association response");
  return result;
}

export function cleanRepositoryDraft(rows: TaskRepositoryInput[]): TaskRepositoryInput[] {
  return rows.map((row) => ({ ...row, links: (row.links ?? [])
    .filter((link) => !("url" in link) || !!link.url?.trim())
    .map((link): RepositoryLinkInput => {
      const title = repositoryLinkTitle(link.title);
      return "url" in link
        ? { url: link.url!.trim(), ...(title ? { title } : {}) }
        : { kind: link.kind, number: link.number, ...(title ? { title } : {}) };
    }) }));
}

/** Explicit association creation stays outside the ordinary Todo client's startup path. */
export async function createTodoWithRepositories(context: RepositoryRequestContext, title: string, notes: string, due: { due_at?: string; due_date?: string } | undefined, bindings: CreateRepositoryBindings): Promise<Todo> {
  const result = await api<{ todo: Todo }>("/todos", {
    ...repositoryRequestOptions(context), method: "POST",
    json: { title: title.trim(), notes: notes.trim() || undefined, ...due, repository_bindings: { ...bindings, repositories: cleanRepositoryDraft(bindings.repositories) } },
  });
  if (!context.isCurrent() || !result.todo || typeof result.todo.id !== "string") throw new Error("Invalid Todo creation response");
  return result.todo;
}
