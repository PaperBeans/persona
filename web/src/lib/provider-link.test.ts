// @vitest-environment jsdom
// Provider import links (#5420): the links the web builds, byte for byte as spec/fixtures/provider-import-link has them
// (the hub reads the same cases back), what it leaves out when a provider's models don't fit, the key it takes, and the
// link it takes out of the address bar when a page opens with one.
import { afterEach, describe, expect, it } from "vitest";
import fixture from "../../../spec/fixtures/provider-import-link/cases.json";
import { takeToken } from "./auth";
import { dropImportLink, holdImportLink, importLinkHeld, onImportLink, restoreImportLink, takeImportLink, takeImportLinkOnStart } from "./import-link";
import { encodeImportData, importLinkHref, linkable, linkKey, linkPayload, type LinkPayload, type LinkSource } from "./provider-link";
import type { ProviderModel } from "./providers";

interface EncodeCase {
  name: string;
  provider: LinkSource;
  key?: string;
  payload: LinkPayload | null;
  fragment?: string;
}

/** The payload a fragment carries, decoded as the hub does it. */
function decode(data: string): unknown {
  const bin = atob(data.replace(/-/g, "+").replace(/_/g, "/"));
  return JSON.parse(new TextDecoder().decode(Uint8Array.from(bin, (c) => c.charCodeAt(0))));
}

const GATEWAY: LinkSource = { name: "gw", display_name: "Gateway", source: "persona", kind: "launcher", agent: "claude", base_url: "https://gateway.example.invalid" };

describe("the links the web builds (spec/fixtures/provider-import-link encode)", () => {
  for (const c of fixture.encode as unknown as EncodeCase[]) {
    it(c.name, () => {
      const built = linkPayload(c.provider, c.key);
      expect(linkable(c.provider)).toBe(c.payload !== null);
      if (c.payload === null) return expect(built).toBeNull();
      expect(built!.payload).toEqual(c.payload);
      // the key order counts: the fixture is the serialized text
      expect(JSON.stringify(built!.payload)).toBe(JSON.stringify(c.payload));
      expect(`import=${encodeImportData(built!.payload)}`).toBe(c.fragment);
      expect(built!.fit).toBe("full");
    });
  }

  it("decodes back to the payload, with no padding and only the base64url alphabet", () => {
    const { payload } = linkPayload({ ...GATEWAY, display_name: "Ünïcode ✓ 网关", model: "m-?" })!;
    const data = encodeImportData(payload);
    expect(data).toMatch(/^[A-Za-z0-9_-]+$/);
    expect(decode(data)).toEqual(payload);
  });

  it("puts the link on this hub's settings page, its Model providers tab", () => {
    expect(importLinkHref("eyJr")).toBe(`${location.origin}/settings?tab=models#import=eyJr`);
  });
});

describe("which providers can be shared", () => {
  it("takes Persona's Claude Code gateways and direct Responses and Chat Completions APIs on a plain address", () => {
    expect(linkable(GATEWAY)).toBe(true);
    expect(linkable({ ...GATEWAY, kind: undefined, agent: undefined })).toBe(true);
    expect(linkable({ ...GATEWAY, source: "jarvis" })).toBe(true);
    for (const api of ["responses", "chat"] as const) expect(linkable({ ...GATEWAY, kind: "api", agent: undefined, api })).toBe(true);
  });

  it("leaves out a direct Messages API: its link would come back as a Claude Code gateway", () => {
    expect(linkable({ ...GATEWAY, kind: "api", agent: undefined, api: "messages" })).toBe(false);
    expect(linkable({ ...GATEWAY, kind: "api", agent: undefined, api: undefined })).toBe(false);
    expect(linkPayload({ ...GATEWAY, kind: "api", api: "messages" })).toBeNull();
  });

  it("leaves out other runtimes, a computer's own launchers and addresses that can hold a key", () => {
    for (const agent of ["codex", "agy", "omp"]) expect(linkable({ ...GATEWAY, agent }), agent).toBe(false);
    for (const source of ["zshrc", "path", "builtin", "agy", undefined]) expect(linkable({ ...GATEWAY, source }), String(source)).toBe(false);
    for (const base_url of [undefined, "", "gateway.example.invalid", "ftp://gateway.example.invalid", "https://user:pw@gateway.example.invalid", "https://gateway.example.invalid/?key=x", "https://gateway.example.invalid/#k", "https://gateway.example.invalid/a b"])
      expect(linkable({ ...GATEWAY, base_url }), String(base_url)).toBe(false);
  });
});

describe("the payload", () => {
  it("names the provider by its display name, cut to 60 UTF-16 units without splitting a character", () => {
    const at = (display_name: string) => linkPayload({ ...GATEWAY, display_name })!.payload.name;
    expect(at("  Spaced  ")).toBe("Spaced");
    expect(at("x".repeat(60))).toBe("x".repeat(60));
    expect(at("x".repeat(61))).toBe("x".repeat(60));
    // the emoji's two units would be the 60th and 61st: it goes whole
    expect(at(`${"x".repeat(59)}😀tail`)).toBe("x".repeat(59));
    expect(at(`${"x".repeat(58)}😀tail`)).toBe(`${"x".repeat(58)}😀`);
    // no display name: the startup ID
    expect(linkPayload({ ...GATEWAY, display_name: "" })!.payload.name).toBe("gw");
  });

  it("carries a key only when one is given", () => {
    expect(linkPayload(GATEWAY)!.payload).not.toHaveProperty("auth");
    expect(linkPayload(GATEWAY, "FAKE-KEY")!.payload.auth).toEqual({ method: "apiKey", apiKey: "FAKE-KEY" });
  });

  it("lists the default first, each model once, and only cindy's thinking levels", () => {
    const models: ProviderModel[] = [
      { id: "a", name: "a" },
      { id: "b", efforts: ["high", "turbo", "high"], default_effort: "turbo" },
      { id: "a" },
      { id: " " },
      { id: "c", context_window: 0, name: " " },
    ];
    const { payload } = linkPayload({ ...GATEWAY, model: "c", models })!;
    expect(payload.endpoints[0]!.models).toEqual(["c", "a", { id: "b", reasoning: true, reasoningEfforts: ["high"] }]);
  });

  it("trims each model ID and counts IDs equal once trimmed as one, as the hub does", () => {
    const { payload } = linkPayload({ ...GATEWAY, model: " m", models: [{ id: "m" }, { id: "m " }, { id: " x ", name: "X" }, { id: "\t" }] })!;
    expect(payload.endpoints[0]!.models).toEqual(["m", { id: "x", name: "X" }]);
  });

  it("brings only the model IDs when their details don't fit, and no list when the IDs don't either", () => {
    const named = (n: number, extra = "") => Array.from({ length: n }, (_, i) => ({ id: `model-${i}${extra}`, name: `A long model name number ${i} ${"z".repeat(80)}`, context_window: 200_000 }));
    const ids = linkPayload({ ...GATEWAY, models: named(200) })!;
    expect(ids.fit).toBe("ids");
    expect(ids.payload.endpoints[0]!.models).toEqual(named(200).map((m) => m.id));

    const none = linkPayload({ ...GATEWAY, models: named(200, "-".repeat(200)) })!;
    expect(none.fit).toBe("none");
    expect(none.payload.endpoints[0]).not.toHaveProperty("models");

    // more than 256 never go, even as IDs
    const many = linkPayload({ ...GATEWAY, models: Array.from({ length: 257 }, (_, i) => ({ id: `m${i}` })) })!;
    expect(many.fit).toBe("none");
    expect(linkPayload({ ...GATEWAY, models: Array.from({ length: 256 }, (_, i) => ({ id: `m${i}` })) })!.fit).toBe("full");
  });

  it("keeps every link within what the hub reads: the fragment and its JSON, a key included", () => {
    const key = "k".repeat(4096);
    for (const n of [100, 150, 200, 256]) {
      const models = Array.from({ length: n }, (_, i) => ({ id: `${"i".repeat(60)}-${i}`, name: `name ${i} ${"n".repeat(60)}` }));
      for (const k of [undefined, key]) {
        const { payload } = linkPayload({ ...GATEWAY, models }, k)!;
        const data = encodeImportData(payload);
        expect(`import=${data}`.length).toBeLessThanOrEqual(32 * 1024);
        expect(new TextEncoder().encode(JSON.stringify(payload)).length).toBeLessThanOrEqual(24 * 1024);
      }
    }
  });
});

describe("the key typed into the copy dialog", () => {
  it("takes a key trimmed, and nothing with spaces, line breaks or control characters inside, or too long", () => {
    expect(linkKey("  FAKE-KEY \n")).toBe("FAKE-KEY");
    for (const bad of ["", "   ", "FAKE KEY", "FAKE\nKEY", "FAKE\tKEY", "FAKE\u0000KEY", "FAKE\u007fKEY", "FAKE KEY", "k".repeat(4097)]) expect(linkKey(bad), JSON.stringify(bad)).toBeNull();
    expect(linkKey("k".repeat(4096))).toBe("k".repeat(4096));
  });
});

describe("a page opened with an import link", () => {
  afterEach(() => dropImportLink());
  const O = "https://persona.example.invalid";

  it("takes the whole link and keeps the settings page's query, dropping the fragment", () => {
    expect(takeImportLink(`${O}/settings?tab=models#import=eyJr&v=1`)).toEqual({ text: `${O}/settings?tab=models#import=eyJr&v=1`, rest: "/settings?tab=models" });
    expect(takeImportLink(`${O}/settings?tab=models&manager=m1#v=1&import=eyJr`)?.rest).toBe("/settings?tab=models&manager=m1");
  });

  it("keeps whatever else the fragment holds for whoever takes it, the link's text whole", () => {
    expect(takeImportLink(`${O}/settings?tab=models#token=jp_x&import=eyJr&v=1`)).toEqual({ text: `${O}/settings?tab=models#token=jp_x&import=eyJr&v=1`, rest: "/settings?tab=models#token=jp_x" });
    expect(takeImportLink(`${O}/overview#import=eyJr&hubs=h1`)?.rest).toBe("/settings?tab=models#hubs=h1");
  });

  it("leaves the address bar as the page starts, a sign-in link's token still there to sign in with", () => {
    history.replaceState(null, "", "/s/abc?x=1#token=jp_x&import=eyJr&v=1");
    const href = location.href;
    takeImportLinkOnStart();
    expect(location.pathname + location.search + location.hash).toBe("/settings?tab=models#token=jp_x");
    expect(importLinkHeld()).toBe(href);
    expect(takeToken(location.href)).toEqual({ token: "jp_x", rest: "/settings?tab=models" });
    // nothing to take: nothing changes
    dropImportLink();
    history.replaceState(null, "", "/settings?tab=models#settings-providers");
    takeImportLinkOnStart();
    expect(location.hash).toBe("#settings-providers");
    expect(importLinkHeld()).toBeNull();
  });

  it("puts a link still waiting back in the address bar for a reload, and nothing when none waits", () => {
    history.replaceState(null, "", "/settings?tab=models");
    restoreImportLink();
    expect(location.pathname + location.search + location.hash).toBe("/settings?tab=models");
    holdImportLink(`${location.origin}/settings?tab=models#import=eyJr`);
    restoreImportLink();
    expect(location.pathname + location.search + location.hash).toBe("/settings?tab=models#import=eyJr");
  });

  it("tells a page already open on the providers about each link held", () => {
    const seen: string[] = [];
    const stop = onImportLink((l) => seen.push(l));
    holdImportLink("a");
    stop();
    holdImportLink("b");
    expect(seen).toEqual(["a"]);
  });

  it("opens the Model providers tab from another page or another tab", () => {
    expect(takeImportLink(`${O}/#import=eyJr`)?.rest).toBe("/settings?tab=models");
    expect(takeImportLink(`${O}/s/abc?x=1#import=eyJr`)?.rest).toBe("/settings?tab=models");
    expect(takeImportLink(`${O}/settings#import=eyJr`)?.rest).toBe("/settings?tab=models");
    expect(takeImportLink(`${O}/settings?tab=general&manager=m1#import=eyJr`)?.rest).toBe("/settings?tab=models&manager=m1");
  });

  it("leaves a page without one alone", () => {
    expect(takeImportLink(`${O}/settings?tab=models`)).toBeNull();
    expect(takeImportLink(`${O}/settings?tab=models#settings-providers`)).toBeNull();
    expect(takeImportLink(`${O}/settings?import=eyJr`)).toBeNull();
    expect(takeImportLink(`${O}/#token=jp_x&hubs=y`)).toBeNull();
    expect(takeImportLink("not a url")).toBeNull();
  });

  it("holds it in memory until it is dropped, reading it as often as asked", () => {
    expect(importLinkHeld()).toBeNull();
    holdImportLink("link");
    expect(importLinkHeld()).toBe("link");
    expect(importLinkHeld()).toBe("link");
    dropImportLink();
    expect(importLinkHeld()).toBeNull();
  });
});
