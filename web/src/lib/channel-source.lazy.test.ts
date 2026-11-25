/// <reference types="node" />
// A message's "From WeChat" mark (SegmentView, on every first screen) needs only the channel list and channelOf.
// Importing them from channels.ts put Settings' channel code (bind, test, state words) and its words there too.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import startup from "../i18n/startup-namespaces.json";
import * as source from "./channel-source";
import * as channels from "./channels";
import type { Channel } from "./channels";

const read = (file: string) => readFileSync(new URL(file, import.meta.url), "utf8");

describe("channel-source", () => {
  it("keeps channels.ts and its words off the first screen", () => {
    expect(read("../views/chat/SegmentView.tsx")).not.toMatch(/^import\s+(?!type\b)[^;]*from\s+["'](\.\.\/)+lib\/channels["']/m);
    expect(read("./channel-source.ts")).not.toMatch(/^import\s+(?!type\b)[^;]*from\s+["']\.\/channels["']/m);
    expect(startup.filter((key) => key === "lib-channels" || key.startsWith("lib-channels."))).toEqual([]);
  });

  it("is the one list Settings forgets", () => {
    // Settings calls forgetChannels from channels.ts; the marks' hook must read the same cache.
    for (const name of ["channelOf", "conversationChannel", "forgetChannels", "listChannels", "useChannels"] as const) {
      expect(channels[name]).toBe(source[name]);
    }
    const list: Pick<Channel, "id" | "name" | "kind">[] = [{ id: "wechat", name: "WeChat", kind: "im" }, { id: "web", name: "Web", kind: "app" }];
    expect(source.channelOf("wechat-1", list)?.name).toBe("WeChat");
    expect(source.channelOf("web-1", list)).toBeUndefined();
    expect(source.channelOf("wechat", list)).toBeUndefined();
    expect(source.conversationChannel(["wechat"], list)?.name).toBe("WeChat");
    expect(source.conversationChannel(["gone"], list)).toEqual({ id: "gone", name: "gone" });
    expect(source.conversationChannel([], list)).toBeUndefined();
  });
});
