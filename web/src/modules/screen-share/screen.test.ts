import { describe, expect, it } from "vitest";
import { accountOf, keepLabel, keysymsOf, screenMachine, screenSocket, screenTarget } from "./screen";

describe("the live screen's page", () => {
  it("is /screen/<machine>", () => {
    expect(screenMachine("/screen/macbook")).toBe("macbook");
    expect(screenMachine("/screen/mac%20mini/")).toBe("mac mini");
    expect(screenMachine("/screen/%ZZ")).toBe("%ZZ");
    expect(screenMachine("/screen")).toBeNull();
    expect(screenMachine("/machines/macbook")).toBeNull();
  });

  it("takes the app's token from the fragment, out of the address bar", () => {
    const { target, rest } = screenTarget("http://hub.example:18660/screen/macbook#token=t0k&embed=1", () => "stored");
    expect(target).toEqual({ mid: "macbook", token: "t0k", embedded: true, name: "macbook", keep: "", unlock: false });
    expect(rest).toBe("/screen/macbook");
  });

  // #1498: the address the app builds (ScreenViewer.url): a device token (jd_…) and the Mac's name, each encoded once.
  it("reads the app's device token and the Mac's name as they were", () => {
    const { target, rest } = screenTarget("http://127.0.0.1:18660/screen/macstudio#token=jd_cafe00a1&name=Mac%20Studio&embed=1", () => "stored");
    expect(target).toEqual({ mid: "macstudio", token: "jd_cafe00a1", embedded: true, name: "Mac Studio", keep: "", unlock: false });
    expect(rest).toBe("/screen/macstudio");
    const odd = screenTarget("http://127.0.0.1/screen/m#token=a%2Bb%2Fc%3D%26d&embed=1").target;
    expect(odd?.token).toBe("a+b/c=&d");
  });

  // #1322: the app that remembers the Mac's sign-in says so; a browser never does.
  it("knows when the app keeps the sign-in", () => {
    expect(screenTarget("http://h/screen/m#token=t&keep=phone&embed=1").target?.keep).toBe("phone");
    expect(screenTarget("http://h/screen/m#token=t&keep=mac&embed=1").target?.keep).toBe("mac");
    expect(screenTarget("http://h/screen/m#token=t&keep=phone").target?.keep).toBe("");
    expect(screenTarget("http://h/screen/m#token=t&keep=cloud&embed=1").target?.keep).toBe("");
    expect(keepLabel("phone")).toBe("在这台手机上记住");
    expect(keepLabel("mac")).toBe("在这台 Mac 上记住");
  });

  // #1507 a-2: one-tap unlock only from the app that keeps the sign-in; the password typed as keysyms, then Return.
  it("unlocks only where the app keeps the sign-in, and types the password as keysyms", () => {
    expect(screenTarget("http://h/screen/m#token=t&keep=phone&unlock=1&embed=1").target?.unlock).toBe(true);
    expect(screenTarget("http://h/screen/m#token=t&unlock=1&embed=1").target?.unlock).toBe(false);
    expect(keysymsOf("aZ9!é")).toEqual([0x61, 0x5a, 0x39, 0x21, 0xe9, 0xff0d]);
    expect(keysymsOf("密")).toEqual([0x01000000 + 0x5bc6, 0xff0d]);
  });

  it("uses the browser's own token without one", () => {
    expect(screenTarget("https://hub.example/screen/macbook", () => "stored").target).toEqual({ mid: "macbook", token: "stored", embedded: false, name: "macbook", keep: "", unlock: false });
    expect(screenTarget("https://hub.example/inbox#token=x", () => "stored").target).toBeNull();
  });

  it("connects through the hub's port relay", () => {
    expect(screenSocket({ protocol: "http:", host: "hub.example:18660" }, "mac book", "a&b")).toBe(
      "ws://hub.example:18660/api/v1/machines/mac%20book/ports/5900/connect?token=a%26b",
    );
    expect(screenSocket({ protocol: "https:", host: "hub.example" }, "m", "t")).toMatch(/^wss:\/\//);
  });

  it("guesses the account from the home directory", () => {
    expect(accountOf("/Users/me")).toBe("me");
    expect(accountOf("/home/me")).toBe("");
    expect(accountOf(undefined)).toBe("");
  });
});
