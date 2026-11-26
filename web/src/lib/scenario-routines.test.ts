import { describe, expect, it } from "vitest";
import { initI18n, t } from "../i18n";
import { taskBody, validateTask, WORKDAYS } from "./routines";
import {
  defaultScenarioDraft,
  instantiateScenario,
  SCENARIO_TEMPLATES,
  scenarioProblem,
  scenarioSources,
  withScenarioRepeat,
  withScenarioSource,
  type ScenarioCatalog,
  type ScenarioDraft,
  type ScenarioKey,
} from "./scenario-routines";

const tools = (names: string[], subscribable = true) => names.map((name) => ({ name, subscribable }));
const catalog = (connector: string, names: string[], patch: Partial<ScenarioCatalog> = {}): ScenarioCatalog => ({
  connector,
  status: "ok",
  sources: [{ id: connector, name: connector, tools: tools(names) }],
  ...patch,
});
const github = catalog("github", ["search", "read", "diff", "checks"]);
const mail = catalog("mail", ["accounts", "search", "read"]);
const catalogs = [github, mail];
const now = new Date(2026, 10, 2, 10, 0);

const draft = (key: ScenarioKey, patch: Partial<ScenarioDraft> = {}): ScenarioDraft => ({
  ...defaultScenarioDraft(key, catalogs),
  ...patch,
});

describe("scenario routine templates", () => {
  it("offers exactly the three requested scenarios", () => {
    expect(SCENARIO_TEMPLATES.map((template) => template.key)).toEqual(["daily-digest", "inbox-triage", "pr-review"]);
  });

  it.each([
    { key: "daily-digest" as const, source: "persona", repeat: "daily", time: "08:30" },
    { key: "inbox-triage" as const, source: "inbox", repeat: "interval", every_minutes: 60 },
    { key: "pr-review" as const, source: "conversation", repeat: "interval", every_minutes: 120 },
  ])("instantiates $key as a disabled read-only draft through the existing task primitive", ({ key, source, ...schedule }) => {
    const input = key === "pr-review" ? withScenarioSource(draft(key), "conversation") : draft(key);
    expect(input.source).toBe(source);
    expect(scenarioProblem(input, catalogs)).toBeNull();
    const task = instantiateScenario(input, catalogs);
    expect(task).not.toBeNull();
    expect(task).toMatchObject({
      id: "",
      title: t(`assistant-scenario-templates.${key}_title`),
      enabled: false,
      permission_mode: "plan",
      push: "asked",
      inbox: "",
      ...schedule,
    });
    expect(validateTask(task!, now)).toBeNull();
    expect(task!.prompt).toContain(t("assistant-scenario-templates.read_only_safety"));
    expect(task!.prompt).not.toContain("subscription_save");
    const body = taskBody(task!);
    expect(body).toMatchObject({ enabled: false, permission_mode: "plan", push: "asked" });
    for (const field of ["launcher", "model", "effort", "cwd", "execution_machine_id"]) expect(body).not.toHaveProperty(field);
  });

  it("preselects GitHub for PR review only with proven read-only access", () => {
    const capable = defaultScenarioDraft("pr-review", catalogs);
    expect(capable.source).toBe("github");
    expect(scenarioProblem(capable, catalogs)).toBe(t("assistant-scenario-templates.error_repository"));
    const fallback = defaultScenarioDraft("pr-review", []);
    expect(fallback.source).toBe("conversation");
    expect(scenarioProblem(fallback, [])).toBeNull();
    expect(instantiateScenario(fallback, [])?.prompt).toContain(t("assistant-scenario-templates.access_conversation"));
  });

  it("builds a daily digest from the selected Persona scope and schedule", () => {
    const input = draft("daily-digest", { scope: "Decisions and deadlines since yesterday", time: "07:45", weekdays: WORKDAYS });
    const task = instantiateScenario(input, catalogs)!;
    expect(task.prompt).toContain("Decisions and deadlines since yesterday");
    expect(task.prompt).toContain(t("assistant-scenario-templates.access_persona"));
    expect(task).toMatchObject({ repeat: "daily", time: "07:45", weekdays: WORKDAYS });
  });

  it("builds inbox triage with only the mail source's declared read-only tools", () => {
    const input = withScenarioSource(draft("inbox-triage"), "mail");
    input.scope = "Unread messages from the billing mailbox";
    input.every_minutes = 30;
    const task = instantiateScenario(input, catalogs)!;
    expect(task.prompt).toContain("Unread messages from the billing mailbox");
    expect(task.prompt).toContain("mail_search");
    expect(task.prompt).toContain("mail_read");
    expect(task.prompt).not.toContain("mail_send");
    expect(task).toMatchObject({ repeat: "interval", every_minutes: 30 });
  });

  it("builds PR review and follow-up for one validated repository without enabling writes", () => {
    const input = withScenarioSource(draft("pr-review"), "github");
    expect(input.scope).toBe("");
    expect(scenarioProblem(input, catalogs)).toBe(t("assistant-scenario-templates.error_repository"));
    input.scope = "example/project";
    const task = instantiateScenario(input, catalogs)!;
    expect(task.prompt).toContain("example/project");
    for (const name of ["github_search", "github_read", "github_diff", "github_checks"]) expect(task.prompt).toContain(name);
    for (const forbidden of ["github_comment", "github_merge", "github_approve"]) expect(task.prompt).not.toContain(forbidden);
    expect(task).toMatchObject({ enabled: false, permission_mode: "plan", push: "asked" });
  });

  it("keeps model and effort as reviewable task-local Advanced overrides", () => {
    const task = instantiateScenario(draft("daily-digest"), catalogs)!;
    const body = taskBody({ ...task, model: "raw-model-id", effort: "low" });
    expect(body).toMatchObject({ model: "raw-model-id", effort: "low", model_revision: 0 });
    expect(body).not.toHaveProperty("launcher");
  });

  it.each([
    { key: "daily-digest" as const, source: "mail" as const },
    { key: "inbox-triage" as const, source: "mail" as const },
    { key: "pr-review" as const, source: "github" as const },
  ])("explains and blocks $key when $source lacks read-only capabilities", ({ key, source }) => {
    const input = withScenarioSource(draft(key), source);
    const option = scenarioSources(key, []).find((item) => item.key === source)!;
    expect(option.available).toBe(false);
    expect(option.note).toBe(t("assistant-scenario-templates.source_unavailable", { source: option.label, fallback: scenarioSources(key, []).find((item) => item.key === option.fallback)?.label ?? "" }));
    expect(scenarioProblem(input, [])).toBe(option.note);
    expect(instantiateScenario(input, [])).toBeNull();
    const fallback = withScenarioSource(input, option.fallback!);
    expect(scenarioProblem(fallback, [])).toBeNull();
    expect(instantiateScenario(fallback, [])?.prompt).toContain(t("assistant-scenario-templates.read_only_safety"));
  });

  it("rejects write-capable, partially capable, rate-limited, and disconnected catalogs", () => {
    const unsafe = catalog("github", ["search", "read", "diff", "checks"], {
      sources: [{ id: "github", name: "GitHub", tools: tools(["search", "read", "diff", "checks"], false) }],
    });
    const partial = catalog("github", ["search"]);
    const disconnected = { ...github, status: "not_signed_in" };
    const rateLimited = { ...github, status: "rate_limited" };
    for (const candidate of [unsafe, partial, disconnected, rateLimited]) {
      const input = withScenarioSource(draft("pr-review"), "github");
      input.scope = "example/project";
      expect(scenarioProblem(input, [candidate])).toContain("GitHub");
      expect(instantiateScenario(input, [candidate])).toBeNull();
    }
    expect(defaultScenarioDraft("pr-review", [rateLimited]).source).toBe("conversation");
  });

  it("normalizes daily and interval schedules across repeat switches", () => {
    for (const key of ["inbox-triage", "pr-review"] as const) {
      const daily = withScenarioRepeat(draft(key), "daily");
      expect(daily).toMatchObject({ repeat: "daily", time: "09:00", every_minutes: 0 });
      const backToInterval = withScenarioRepeat(daily, "interval");
      expect(backToInterval).toMatchObject({ repeat: "interval", time: "", weekdays: [], every_minutes: 30 });
    }
    const clearedDaily = withScenarioRepeat(draft("daily-digest", { time: "", weekdays: WORKDAYS }), "interval");
    expect(clearedDaily).toMatchObject({ repeat: "interval", time: "", weekdays: [], every_minutes: 30 });
    expect(withScenarioRepeat(clearedDaily, "daily")).toMatchObject({ repeat: "daily", time: "09:00", every_minutes: 0 });
  });

  it("validates scope, repository shape, source ownership, and schedule", () => {
    expect(scenarioProblem(draft("daily-digest", { scope: "  " }), catalogs)).toBe(t("assistant-scenario-templates.error_scope"));
    expect(scenarioProblem(draft("daily-digest", { scope: "x".repeat(501) }), catalogs)).toBe(t("assistant-scenario-templates.error_scope_long"));
    expect(scenarioProblem(draft("pr-review", { source: "mail" }), catalogs)).toBe(t("assistant-scenario-templates.error_source"));
    expect(scenarioProblem(draft("daily-digest", { time: "24:00" }), catalogs)).toBe(t("lib-routines.error_time"));
    expect(scenarioProblem(draft("inbox-triage", { every_minutes: 4 }), catalogs)).toBe(t("lib-routines.error_interval"));
    expect(scenarioProblem(draft("inbox-triage", { every_minutes: 5.5 }), catalogs)).toBe(t("lib-routines.error_interval"));
  });

  it("localizes every title, generated prompt, and capability explanation", async () => {
    try {
      for (const lang of ["en", "zh-Hans", "ja"]) {
        await initI18n(lang);
        for (const template of SCENARIO_TEMPLATES) {
          const input = defaultScenarioDraft(template.key, catalogs);
          if (template.key === "pr-review") input.scope = "example/project";
          expect(t(`assistant-scenario-templates.${template.key}_title`)).not.toBe(`assistant-scenario-templates.${template.key}_title`);
          expect(instantiateScenario(input, catalogs)?.prompt).not.toContain("assistant-scenario-templates.");
          for (const option of scenarioSources(template.key, [])) expect(option.label).not.toContain("assistant-scenario-templates.");
        }
      }
    } finally {
      await initI18n("zh-Hans");
    }
  });
});
