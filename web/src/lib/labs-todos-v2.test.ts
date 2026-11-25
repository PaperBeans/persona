import { afterEach, expect, it, vi } from "vitest";
import { api, ApiError } from "./api";
import { MANAGER_HEADER } from "./manager-choice";
import { parseDocument } from "./configuration/contract";
import { todosV2LabsSource } from "./labs-todos-v2";
import { t } from "../i18n";

vi.mock("./api", async (original) => ({ ...await original<typeof import("./api")>(), api: vi.fn() }));
afterEach(() => vi.resetAllMocks());

it("binds the Labs read, save and reset to the manager whose form was opened", async () => {
  const manager = "example";
  const source = todosV2LabsSource(manager);
  expect(source.key).toBe(`labs:assistant-todos-v2:${manager}`);
  const request = vi.mocked(api);
  request.mockResolvedValueOnce({ manager, todos_v2: false });
  const { doc } = parseDocument(await source.read());
  expect(doc.id).toBe("assistant-todos-v2");
  expect(doc.values.todos_v2).toBe(false);
  expect(doc.groups[0].fields[0].details?.map((d) => d.title)).toEqual([
    t("settings-labs.scope"),
    t("settings-labs.activation"),
    t("settings-labs.rollback"),
  ]);
  expect(request).toHaveBeenCalledWith("/assistant", expect.objectContaining({ headers: { [MANAGER_HEADER]: manager } }));
  request.mockResolvedValueOnce({ manager, todos_v2: true });
  const { doc: saved } = parseDocument(await source.update({ ...doc, changes: { todos_v2: true } }));
  expect(saved.values.todos_v2).toBe(true);
  expect(saved.notices?.todos_v2?.kind).toBe("info");
  expect(request).toHaveBeenLastCalledWith("/assistant", expect.objectContaining({
    headers: { [MANAGER_HEADER]: manager }, json: { todos_v2: true, expected_todos_v2: false },
  }));
  request.mockResolvedValueOnce({ manager, todos_v2: false });
  expect(parseDocument(await source.update({ ...saved, reset: ["todos_v2"] })).doc.values.todos_v2).toBe(false);
  expect(request).toHaveBeenLastCalledWith("/assistant", expect.objectContaining({
    json: { todos_v2: false, expected_todos_v2: true },
  }));
});

it("leaves older managers read-only, reports unconfirmed saves, and surfaces 409 conflicts", async () => {
  const source = todosV2LabsSource("example");
  vi.mocked(api).mockResolvedValueOnce({ manager: "example" });
  const { doc } = parseDocument(await source.read());
  expect(doc.groups[0].fields[0].read_only).toBe(true);
  expect(doc.groups[0].fields[0].disabled_reason).toBe(t("settings-labs.todos_v2_unsupported"));

  vi.mocked(api).mockResolvedValueOnce({ manager: "other", todos_v2: true });
  await expect(source.update({ schema_version: 1, id: doc.id, revision: "false", changes: { todos_v2: true } }))
    .rejects.toMatchObject({ code: "configuration_unavailable" });

  vi.mocked(api).mockRejectedValueOnce(new ApiError(409, "todos_v2 setting changed", { code: "configuration_conflict", error: "todos_v2 setting changed" }));
  await expect(source.update({ schema_version: 1, id: doc.id, revision: "false", changes: { todos_v2: true } }))
    .rejects.toMatchObject({ code: "configuration_conflict" });
});
