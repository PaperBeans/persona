import { createContext, useContext } from "react";

/** The send time of the chat's last message, used to keep a user bubble's time on screen (#5285). */
export const LatestMessageTime = createContext<string | undefined>(undefined);

/** The latest finished reply's item ID (#15428). Only its chat line receives it so earlier lines stay memoized. */
export const LatestReplyId = createContext<string | undefined>(undefined);

/** Whether the message sent at `ts` is the chat's last one. */
export function useIsLatestMessage(ts: string | undefined): boolean {
  const latest = useContext(LatestMessageTime);
  return Boolean(ts) && ts === latest;
}
