// A model provider's key as a secret provider's reference (#3962 step 4c, docs/extensions/secret-providers.md "Launcher
// keys"): the forms send api_key_reference / machine_key_references in place of the key when the field takes a
// reference, a stored reference opens with the switch on, and a reference is never sent to a connection test or a
// model list, which would hand its value to the address in the request.
import { describe, expect, it } from "vitest";
import { presetBody, type LauncherPreset } from "./launcher-presets";
import { newScope, scopeBody, scopeFromView } from "./provider-scope";
import { autoCheckable, formFromView, newForm, probeBody, providerBody, referencePending } from "./providers";
import { referenceState } from "./secret-reference";
import { t } from "../i18n";

const REF = "op://Dev/Gateway/key";
const preset = { id: "example", name: "Example", base_url: "https://api.example.com" } as unknown as LauncherPreset;

describe("a provider's key as a reference", () => {
  it("is sent as api_key_reference in place of the key", () => {
    const f = { ...newForm(), name: "gw", base_url: "https://gw.example.com", api_key: REF, key_by_reference: true };
    const body = providerBody(f, null);
    expect(body.api_key_reference).toBe(REF);
    expect(body).not.toHaveProperty("api_key");
    const typed = providerBody({ ...f, api_key: "sk-1", key_by_reference: false }, null);
    expect(typed.api_key).toBe("sk-1");
    expect(typed).not.toHaveProperty("api_key_reference");
    // nothing typed keeps what is stored, a reference or a key
    expect(providerBody({ ...f, api_key: "" }, null)).not.toHaveProperty("api_key_reference");
  });

  it("is never sent to a test or a model list, which wait for the save", () => {
    const f = { ...newForm(), base_url: "https://gw.example.com", api_key: REF, key_by_reference: true };
    expect(referencePending(f)).toBe(true);
    const b = probeBody(f, "gw");
    expect(b).not.toHaveProperty("api_key");
    expect(b).not.toHaveProperty("api_key_reference");
    expect(autoCheckable(f, null)).toBe(false);
    // a stored reference, nothing typed: probed by name, as a stored key is
    const stored = { ...f, api_key: "" };
    expect(referencePending(stored)).toBe(false);
    expect(probeBody(stored, "gw")).toMatchObject({ name: "gw" });
  });

  it("opens with the switch on when the stored key is a reference", () => {
    const view = { name: "gw", has_key: true, key_reference: { provider: "example-vault", provider_name: "Example Vault", reference: REF, title: "Gateway › key" } };
    const f = formFromView(view);
    expect(f.key_by_reference).toBe(true);
    expect(f.api_key).toBe("");
    expect(formFromView({ name: "gw", has_key: true }).key_by_reference).toBe(false);
  });

  it("goes in a preset's body in place of the key", () => {
    const body = presetBody(preset, { name: "", display_name: "", api_key: REF, key_by_reference: true, model: "" });
    expect(body).toMatchObject({ api_key_reference: REF, preset: "example" });
    expect(body).not.toHaveProperty("api_key");
    expect(presetBody(preset, { name: "", display_name: "", api_key: "sk-1", model: "" })).toMatchObject({ api_key: "sk-1" });
  });

  it("goes for a single computer in machine_key_references, a typed key in machine_keys", () => {
    const machines: Parameters<typeof scopeBody>[2] = [{ id: "laptop" }, { id: "desk" }];
    const f = { ...newScope(), keys: { laptop: REF, desk: "sk-desk" }, key_refs: { laptop: true } };
    const body = scopeBody(f, null, machines);
    expect(body.machine_key_references).toEqual({ laptop: REF });
    expect(body.machine_keys).toEqual({ desk: "sk-desk" });
    // a computer whose stored key is a reference opens with its switch on
    const opened = scopeFromView({ share: "all", key_machines: ["laptop"], machine_key_references: { laptop: { provider: "example-vault", provider_name: "Example Vault", reference: REF } } });
    expect(opened?.key_refs).toEqual({ laptop: true });
  });
});

describe("where a reference's value comes from", () => {
  it("names its provider and item, and only the reference once the provider is gone", () => {
    const from = (provider: string) => t("lib-extensions.from_provider", { provider });
    expect(referenceState({ provider: "example-vault", provider_name: "Example Vault", reference: REF, title: "Gateway › key" })).toBe(`${from("Example Vault")} · Gateway › key`);
    expect(referenceState({ provider: "example-vault", provider_name: "", reference: REF })).toBe(from("example-vault"));
    // a removed provider extension: the hub answers its id and name empty, so no "From  · …"
    expect(referenceState({ provider: "", provider_name: "", reference: REF, title: "" })).toBe(REF);
  });
});
