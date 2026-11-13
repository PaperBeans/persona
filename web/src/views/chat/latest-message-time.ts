import { createContext, useContext } from "react";

/** The send time (`Item.ts`) of the chat's last message: that message keeps its time on screen without a hover (#5285).
 *  Unset outside a chat transcript, where no message is pinned. */
export const LatestMessageTime = createContext<string | undefined>(undefined);

/** Whether the message sent at `ts` is the chat's last one. */
export function useIsLatestMessage(ts: string | undefined): boolean {
  const latest = useContext(LatestMessageTime);
  return Boolean(ts) && ts === latest;
}
