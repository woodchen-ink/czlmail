// Interface language. Chinese is the only one for now; to add English, create en.ts
// exporting a Messages object and pick it here (e.g. from navigator.language after mount).
import { zhCN, type Messages } from "./zh-CN"

export type { Messages }

const messages: Messages = zhCN

export const locale = "zh-CN"

/** The current messages. A hook so a language switch can later re-render through context. */
export function useT(): Messages {
  return messages
}

/** For code outside React (the API error handler). */
export function t(): Messages {
  return messages
}
