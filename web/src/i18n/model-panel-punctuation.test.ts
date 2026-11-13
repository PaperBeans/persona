// The model pill's and panel's Chinese and Japanese words (#5032, #5054) use full-width punctuation between CJK words, as
// their neighbours do ("已登记切换：…", "切り替えを予約しました：…"): an ASCII colon, semicolon, question or exclamation mark
// beside a CJK character is a slip (copied text). Scoped to these catalogs: elsewhere `主机:端口` is meant.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const CJK = "぀-ヿ㐀-鿿";
const SLIP = new RegExp(`[${CJK}][:;?!]|[:;?!][${CJK}]`);

describe("full-width punctuation in the model pill and panel", () => {
  for (const language of ["zh-Hans", "ja"]) {
    for (const namespace of ["chat-modelpanel", "chat-modelpill"]) {
      it(`${language}/${namespace}`, () => {
        const strings: Record<string, string> = JSON.parse(readFileSync(new URL(`./locales/${language}/${namespace}.json`, import.meta.url), "utf8"));
        expect(Object.entries(strings).filter(([, v]) => SLIP.test(v)).map(([k, v]) => `${k}: ${v}`)).toEqual([]);
      });
    }
  }
});
