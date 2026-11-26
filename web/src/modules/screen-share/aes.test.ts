import { describe, expect, it } from "vitest";
import { cbcEncrypt, encryptBlock, ensureSubtleAES, expandKey } from "./aes";

const hex = (s: string) => Uint8Array.from(s.match(/../g)!.map((b) => parseInt(b, 16)));
const toHex = (b: Uint8Array) => Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");

describe("AES for noVNC without crypto.subtle", () => {
  // FIPS-197 appendix C.
  it.each([
    ["000102030405060708090a0b0c0d0e0f", "69c4e0d86a7b0430d8cdb78070b4c55a"],
    ["000102030405060708090a0b0c0d0e0f1011121314151617", "dda97ca4864cdfe06eaf70a0ec0d7191"],
    ["000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", "8ea2b7ca516745bfeafc49904b496089"],
  ])("encrypts FIPS-197's block with a %s-byte key", (key, want) => {
    const block = hex("00112233445566778899aabbccddeeff");
    encryptBlock(expandKey(hex(key)), block);
    expect(toHex(block)).toBe(want);
  });

  it("is AES-CBC with PKCS#7 padding, as WebCrypto's", async () => {
    const key = globalThis.crypto.getRandomValues(new Uint8Array(16));
    const iv = new Uint8Array(16);
    const data = globalThis.crypto.getRandomValues(new Uint8Array(128)); // an ARD credential block
    const real = new Uint8Array(await globalThis.crypto.subtle.encrypt({ name: "AES-CBC", iv },
      await globalThis.crypto.subtle.importKey("raw", key, { name: "AES-CBC" }, false, ["encrypt"]), data));
    expect(toHex(cbcEncrypt(expandKey(key), iv, data))).toBe(toHex(real));
  });

  it("stands in only where there is no subtle", async () => {
    const real = { subtle: {} };
    expect(ensureSubtleAES(real)).toBe(false);
    const insecure: { subtle?: SubtleCrypto } = {};
    expect(ensureSubtleAES(insecure)).toBe(true);
    const key = await insecure.subtle!.importKey("raw", hex("000102030405060708090a0b0c0d0e0f"), { name: "AES-CBC" }, false, ["encrypt"]);
    const out = new Uint8Array(await insecure.subtle!.encrypt({ name: "AES-CBC", iv: new Uint8Array(16) }, key, hex("00112233445566778899aabbccddeeff")));
    expect(toHex(out.slice(0, 16))).toBe("69c4e0d86a7b0430d8cdb78070b4c55a"); // noVNC's ECB: CBC from a zero IV, first block
  });
});
