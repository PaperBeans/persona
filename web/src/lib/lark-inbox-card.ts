// lark-cli serializes a single interactive message as <card title="...">.
// Only decode that envelope, never infer message boundaries from Inbox context.
export interface LarkInboxCard {
  title: string;
  body: string;
  request?: { title: string; url: string };
  repository?: { title: string; url: string };
  author?: string;
  description?: string;
}

function sourceLink(text: string): { title: string; url: string } | undefined {
  const link = /^\[([^\n]+)\]\((https?:\/\/[^\s)]+)\)$/.exec(text.trim());
  if (!link) return;
  try {
    const url = new URL(link[2]);
    if (url.username || url.password) return;
    return { title: link[1], url: url.href };
  } catch { return; }
}

export function larkInboxCard(text: string): LarkInboxCard | undefined {
  const card = /^\s*<card(?:\s+title="([^"<>]*)")?\s*>([\s\S]*)<\/card>\s*$/.exec(text);
  if (!card || /<\/?card(?:\s|>)/i.test(card[2])) return;
  const result: LarkInboxCard = { title: card[1] ?? "", body: card[2].trim() };
  // This known notification has a strict four-field Markdown contract. Other
  // cards keep their body intact instead of being rearranged speculatively.
  const fields = [...result.body.matchAll(/^\*\*(Merge Request|Author|Repository|Description)\*\*\s*$/gm)];
  if (fields.map((m) => m[1]).join(",") !== "Merge Request,Author,Repository,Description" || fields[0].index !== 0) return result;
  const values = fields.map((field, i) => result.body.slice(field.index! + field[0].length, fields[i + 1]?.index ?? result.body.length).trim());
  const request = sourceLink(values[0]), repository = sourceLink(values[2]);
  if (!request || !repository || values[1].includes("\n")) return result;
  return { ...result, request, repository, author: values[1], description: values[3] };
}

export function larkMessagePreview(text: string): string {
  const card = larkInboxCard(text);
  return (card?.request?.title || card?.title || text).replace(/\s+/g, " ").slice(0, 160);
}
