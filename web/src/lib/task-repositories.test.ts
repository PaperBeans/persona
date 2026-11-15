// @vitest-environment jsdom
import { expect, it } from "vitest";
import fixture from "../../../spec/fixtures/task-repositories/contract.json";
import { cleanRepositoryDraft, repositoryDraft, repositoryLinkTitle, repositoryLinkView, repositoryRelativePath, validRepositoryAlias, validRepositorySet, validRequestedRef, validTaskRepositoryLink, type RequestedRef, type TaskRepository, type TaskRepositorySet } from "./task-repositories";

it("shares alias, layout and typed-ref fixtures with the manager", () => {
  for (const alias of fixture.aliases.valid) expect(validRepositoryAlias(alias), alias).toBe(true);
  for (const alias of fixture.aliases.invalid) expect(validRepositoryAlias(alias), alias).toBe(false);
  for (const ref of fixture.refs) expect(validRequestedRef(ref as RequestedRef)).toBe(true);
  expect(repositoryRelativePath("root", "", 1)).toBe(".");
  expect(repositoryRelativePath("root", "", 2)).toBeUndefined();
  expect(repositoryRelativePath("container", "web", 1)).toBe("web");
});

it("preserves typed refs and numeric string links when round-tripping a saved repository", () => {
  const row = { repo_id: "web", requested_ref: fixture.refs[3], links: [{ kind: "pull_request", number: "9007199254740993", canonical_url: "https://github.com/example/web/pull/9007199254740993" }] } as TaskRepository;
  expect(repositoryDraft([row])[0]).toMatchObject({ requested_ref: fixture.refs[3], links: [{ kind: "pull_request", number: "9007199254740993" }] });
  expect(cleanRepositoryDraft([{ repo_id: "web", links: [{ url: "  https://github.com/example/web/pull/7  " }, { url: "" }] }])[0].links).toEqual([{ url: "https://github.com/example/web/pull/7" }]);
});

const github = { provider: "github", instance_url: "https://github.com", path: "example/web", canonical_url: "https://github.com/example/web" };
const gitlab = { provider: "gitlab", instance_url: "https://gitlab.example.com", path: "team/api", canonical_url: "https://gitlab.example.com/team/api" };

it("reads a pasted issue or PR URL only when it belongs to the saved repository", () => {
  expect(repositoryLinkView(github, { url: "https://github.com/example/web/pull/7" })).toEqual({ kind: "pull_request", number: "7", url: "https://github.com/example/web/pull/7", label: "PR #7" });
  expect(repositoryLinkView(github, { url: "https://github.com/example/web/issues/12" })).toMatchObject({ kind: "issue", number: "12", label: "#12" });
  // Another repository, another host, another scheme, credentials, a query, a fragment, a deeper path or a non-number: all refused.
  const credentials = "https://user:pw" + "@github.com/example/web/pull/7";
  for (const url of ["https://github.com/example/docs/pull/7", "https://gitlab.com/example/web/pull/7", "http://github.com/example/web/pull/7",
    credentials, "https://github.com/example/web/pull/7?x=1", "https://github.com/example/web/pull/7#c",
    "https://github.com/example/web/pull/7/files", "https://github.com/example/web/pull/", "https://github.com/example/web/pull/07",
    "https://github.com/example/web/pull/0", "https://github.com/example/web", "not a url", ""]) {
    expect(repositoryLinkView(github, { url }), url).toBeNull();
  }
});

it("reads saved kind and number for GitHub and GitLab, keeping the number a string", () => {
  expect(repositoryLinkView(gitlab, { kind: "pull_request", number: "9" })).toEqual({ kind: "pull_request", number: "9", url: "https://gitlab.example.com/team/api/-/merge_requests/9", label: "MR !9" });
  expect(repositoryLinkView(gitlab, { kind: "issue", number: "9" })).toMatchObject({ url: "https://gitlab.example.com/team/api/-/issues/9", label: "#9" });
  expect(repositoryLinkView(gitlab, { url: "https://gitlab.example.com/team/api/-/merge_requests/9007199254740993" })).toMatchObject({ number: "9007199254740993" });
  expect(repositoryLinkView(github, { kind: "issue", number: "0" })).toBeNull();
  expect(repositoryLinkView({ ...github, provider: "bitbucket" }, { kind: "issue", number: "1" })).toBeNull();
});

it("carries a full source title on the exact saved link, never onto another repository or number", () => {
  const link = { kind: "pull_request" as const, number: "7", canonical_url: "https://github.com/example/web/pull/7", title: "  Fix the login redirect  " };
  expect(repositoryLinkView(github, link)).toEqual({ kind: "pull_request", number: "7", url: "https://github.com/example/web/pull/7", label: "PR #7", title: "Fix the login redirect" });
  const elsewhere = { ...link, canonical_url: "https://github.com/example/docs/pull/7" };
  expect(repositoryLinkView(github, elsewhere)).toBeNull();
  expect(repositoryLinkView(github, { ...link, title: "two\nlines" })).toEqual({ kind: "pull_request", number: "7", url: "https://github.com/example/web/pull/7", label: "PR #7" });
});

it("accepts the same title bounds as the server and keeps titles through a draft round trip", () => {
  expect(repositoryLinkTitle("  A title  ")).toBe("A title");
  expect(repositoryLinkTitle("x".repeat(1024))).toHaveLength(1024);
  expect(repositoryLinkTitle("x".repeat(1025))).toBeUndefined();
  expect(repositoryLinkTitle("é".repeat(512))).toHaveLength(512);
  expect(repositoryLinkTitle("é".repeat(513))).toBeUndefined();
  expect(repositoryLinkTitle("a\u0000b")).toBeUndefined();
  expect(repositoryLinkTitle("   ")).toBeUndefined();
  const row = { repo_id: "web", requested_ref: { kind: "default" }, links: [
    { kind: "pull_request", number: "7", canonical_url: "https://github.com/example/web/pull/7", title: "Fix the redirect" },
    { kind: "issue", number: "8", canonical_url: "https://github.com/example/web/issues/8", title: "Reported crash" },
  ] } as TaskRepository;
  expect(repositoryDraft([row])[0].links).toEqual([
    { kind: "pull_request", number: "7", title: "Fix the redirect" },
    { kind: "issue", number: "8", title: "Reported crash" },
  ]);
  expect(cleanRepositoryDraft([{ repo_id: "web", links: [
    { kind: "pull_request", number: "7", title: "  Fix the redirect  " },
    { url: "https://github.com/example/web/issues/8", title: "Reported crash" },
    { kind: "issue", number: "9", title: "x".repeat(1025) },
  ] }])[0].links).toEqual([
    { kind: "pull_request", number: "7", title: "Fix the redirect" },
    { url: "https://github.com/example/web/issues/8", title: "Reported crash" },
    { kind: "issue", number: "9" },
  ]);
});

it("validates a saved response's link against the repository it claims", () => {
  const link = { kind: "pull_request" as const, number: "7", canonical_url: "https://github.com/example/web/pull/7", title: "Fix the redirect" };
  const envelope = (links: TaskRepositorySet["repositories"][number]["links"]): TaskRepositorySet => ({ schema_version: 1, manager: "assistant", todo_id: "todo", revision: 1,
    repositories: [{ repo_id: "web", requested_ref: { kind: "default" }, source_snapshot: { identity: github, clone_url: github.canonical_url, transport_origin: "canonical_default", catalog_revision: 1 }, links }] });
  expect(validTaskRepositoryLink(github, link)).toBe(true);
  expect(validRepositorySet(envelope([link]), "assistant", "todo")).toBe(true);
  expect(validTaskRepositoryLink(github, { ...link, canonical_url: "https://github.com/example/docs/pull/7" })).toBe(false);
  expect(validRepositorySet(envelope([{ ...link, canonical_url: "https://github.com/example/docs/pull/7" }]), "assistant", "todo")).toBe(false);
  expect(validRepositorySet(envelope([{ ...link, title: "bad\u0000title" }]), "assistant", "todo")).toBe(false);
  expect(validRepositorySet(envelope([{ ...link, title: " padded " }]), "assistant", "todo")).toBe(false);
});
