import { describe, expect, it } from "vitest";
import { startupMatcher } from "./startup-keys";
import startup from "./startup-namespaces.json";
import modelIdMessages from "./locales/en/lib-model-id.json";
import emptyResponseMessages from "./locales/en/chat-empty-response.json";
import hubSettingsMessages from "./locales/en/settings-hubsettings.json";
import notifyMessages from "./locales/en/notify.json";
import assistantViewMessages from "./locales/en/lib-assistant-view.json";
import assistantCardMessages from "./locales/en/ui-assistantcard.json";

describe("startupMatcher", () => {
  const inStartup = startupMatcher(["common", "settings.avatar", "todos.stage_"]);

  it("takes a whole namespace", () => {
    expect(inStartup("common.ok")).toBe(true);
    expect(inStartup("commons.ok")).toBe(false);
  });

  it("takes every key that begins with a listed one: its plural forms and suffixes too", () => {
    for (const key of ["settings.avatar", "settings.avatar_one", "settings.avatar_hint", "todos.stage_plan", "todos.stage_pr"]) expect(inStartup(key), key).toBe(true);
    for (const key of ["settings.theme", "settings.avata", "todos.stage", "todos.kicker_plan", "other.avatar"]) expect(inStartup(key), key).toBe(false);
  });

  it("takes nothing from an empty list", () => {
    expect(startupMatcher([])("common.ok")).toBe(false);
  });

  it("loads model ID hints and validation messages with the model editors", () => {
    const configured = startupMatcher(startup);
    for (const key of Object.keys(modelIdMessages)) expect(configured(`lib-model-id.${key}`), key).toBe(false);
  });

  it("keeps empty-response copy out of the first-screen catalogs", () => {
    const configured = startupMatcher(startup);
    for (const key of Object.keys(emptyResponseMessages)) expect(configured(`chat-empty-response.${key}`), key).toBe(false);
  });

  it("loads update pipeline copy with Hub settings", () => {
    const configured = startupMatcher(startup);
    const pipelineKeys = Object.keys(hubSettingsMessages).filter(key => /^(pipeline|progress)_/.test(key));
    expect(pipelineKeys).toContain("pipeline_title");
    for (const key of pipelineKeys) expect(configured(`settings-hubsettings.${key}`), key).toBe(false);
  });

  it("keeps reply translation feedback eager and menu-only words in the intent-loaded catalog", () => {
    const configured = startupMatcher(startup);
    for (const key of [
      "chat-messageactions.translate", "chat-messageactions.translating", "chat-messageactions.show_original",
      "chat-messageactions.copy", "rewind.editing", "rewind.unsupported",
    ]) expect(configured(key), key).toBe(true);
    for (const key of [
      "chat-messageactions.translation_failed", "chat-messageactions.done", "chat-messageactions.save_as_prompt",
      "chat-messageactions.fork", "chat-messageactions.fork_chat",
      "chat-messageactions.select_text", "chat-messageactions.pick",
      "rewind.menu", "rewind.conversation", "rewind.code_and_conversation", "rewind.confirm_code",
    ]) expect(configured(key), key).toBe(false);
  });

  it("keeps main-chat header feedback eager without loading the new-main dialog's words", () => {
    const configured = startupMatcher(startup);
    for (const key of [
      "back_to_main", "clear", "clear_failed", "close", "compact", "compacting", "compacted",
      "compact_failed", "compact_start_failed", "copy_info", "details", "knowledge", "new_main_ellipsis", "new_main_elsewhere",
    ]) expect(configured(`assistant-assistantchat.${key}`), key).toBe(true);
    for (const key of [
      "new_main_done_carried", "new_main_done", "new_main_failed", "new_main_title", "new_main", "new_main_body",
      "carry_summary", "carry_summary_hint",
    ]) expect(configured(`assistant-assistantchat.${key}`), key).toBe(false);
  });

  it("keeps notification copy out of the first-screen startup catalog", () => {
    const configured = startupMatcher(startup);
    expect(startup).not.toContain("notify");
    for (const key of Object.keys(notifyMessages)) expect(configured(`notify.${key}`), key).toBe(false);
  });

  it("defers assistant-view and body-only assistantcard strings while keeping shell and bubble keys eager", () => {
    const configured = startupMatcher(startup);
    for (const key of Object.keys(assistantViewMessages)) expect(configured(`lib-assistant-view.${key}`), key).toBe(false);
    const eagerCardKeys = new Set(["cannot_open", "from_card"]);
    for (const key of Object.keys(assistantCardMessages)) {
      expect(configured(`ui-assistantcard.${key}`), key).toBe(eagerCardKeys.has(key));
    }
  });
});
