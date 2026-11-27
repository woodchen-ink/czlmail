"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";

import { t } from "@/lib/i18n";
import { ACCOUNT_COLORS, accountColorValue } from "@/lib/account-looks";
import { Events, api, onEvent, type EmailSummary } from "@/lib/api";
import type { main } from "@/wailsjs/go/models";

/**
 * 标签与待办旗标。
 *
 * 邮件上的标签是服务器上的关键字 (只能是 ASCII, 多被转成小写); 显示名与颜色是本程序另记的定义
 * (设置 → 标签, 经个人网盘同步, 见后端 message_tags.go)。没有定义的关键字照样显示, 用关键字本身作名字、不带颜色。
 *
 * 待办旗标 (设置 → 标签里打开) 用 $flagged 表示待办, $completed 表示已完成; 关着时 $flagged 照旧显示为星标。
 */
export type MessageTag = main.MessageTag;

export type TaskFlagState = "none" | "flagged" | "completed";

export function taskFlagState(e: Pick<EmailSummary, "isFlagged" | "isCompleted">): TaskFlagState {
  if (e.isFlagged) return "flagged";
  if (e.isCompleted) return "completed";
  return "none";
}

/** 点一下旗标之后的状态, 同 Outlook: 没有 → 待办 → 完成, 完成的再点回到待办; 清除走菜单。 */
export function nextTaskFlag(state: TaskFlagState): TaskFlagState {
  return state === "flagged" ? "completed" : "flagged";
}

/** 旗标状态对应的乐观更新字段。 */
export function taskFlagPatch(state: TaskFlagState): Pick<EmailSummary, "isFlagged" | "isCompleted"> {
  return { isFlagged: state === "flagged", isCompleted: state === "completed" };
}

export function taskFlagTitle(state: TaskFlagState): string {
  if (state === "flagged") return t("待办，点击标记为已完成");
  if (state === "completed") return t("已完成，点击重新标记为待办");
  return t("标记为待办");
}

export interface MessageMarks {
  /** 定义过名字或颜色的标签, 按名字排序。 */
  tags: MessageTag[];
  byKeyword: Record<string, MessageTag>;
  /** 设置 → 标签 →「用旗标标记待办」。 */
  taskFlags: boolean;
  reload: () => Promise<void>;
}

const empty: MessageMarks = { tags: [], byKeyword: {}, taskFlags: false, reload: async () => {} };

export const MessageMarksContext = createContext<MessageMarks>(empty);

/** 邮件界面里读标签定义与旗标设置; 由 MailShell 用 useMessageMarksState 读一次后经 context 提供。 */
export function useMessageMarks(): MessageMarks {
  return useContext(MessageMarksContext);
}

/** 读标签定义与旗标设置; 本机修改与别的设备同步过来时 (都发 settings:changed) 跟着更新。 */
export function useMessageMarksState(): MessageMarks {
  const [tags, setTags] = useState<MessageTag[]>([]);
  const [taskFlags, setTaskFlags] = useState(false);
  const reload = useCallback(
    () =>
      Promise.all([api.getMessageTags().catch(() => null), api.getSettings().catch(() => null)]).then(
        ([list, settings]) => {
          if (list) setTags(list);
          if (settings) setTaskFlags(!!settings.taskFlags);
        },
      ),
    [],
  );
  useEffect(() => {
    void reload();
    return onEvent(Events.settingsChanged, () => void reload());
  }, [reload]);
  return useMemo(
    () => ({ tags, byKeyword: Object.fromEntries(tags.map((g) => [g.keyword, g])), taskFlags, reload }),
    [tags, taskFlags, reload],
  );
}

/** 标签的显示名: 有定义用定义的名字, 否则是关键字本身。 */
export function tagName(keyword: string, byKeyword: Record<string, MessageTag>): string {
  return byKeyword[keyword]?.name || keyword;
}

/** 标签颜色的色值; 没有定义或没挑颜色时为 undefined。 */
export function tagColor(keyword: string, byKeyword: Record<string, MessageTag>): string | undefined {
  return accountColorValue(byKeyword[keyword]?.color);
}

/** 新标签默认用还没被用过 (或用得最少) 的颜色, 按调色板顺序。 */
export function nextTagColor(tags: MessageTag[]): string {
  const used = new Map(ACCOUNT_COLORS.map((c) => [c.key, 0]));
  for (const g of tags) if (used.has(g.color ?? "")) used.set(g.color!, used.get(g.color!)! + 1);
  const least = Math.min(...used.values());
  return ACCOUNT_COLORS.find((c) => used.get(c.key) === least)!.key;
}

export interface TagOption {
  keyword: string;
  name: string;
  color?: string;
  on: boolean;
}

/**
 * 标签菜单里的选项: 定义过的标签 (按名字), 再加上邮件上或账号里出现过、但没有定义的关键字。
 * current 为 null 表示还不知道邮件上有哪些标签 (都不勾)。
 */
export function tagOptions(marks: MessageMarks, current: string[] | null, known: string[] = []): TagOption[] {
  const on = new Set(current ?? []);
  const defined = marks.tags.map((g) => ({ keyword: g.keyword, name: g.name || g.keyword, color: accountColorValue(g.color), on: on.has(g.keyword) }));
  const extra = [...new Set([...known, ...(current ?? [])])]
    .filter((k) => !marks.byKeyword[k])
    .sort()
    .map((k) => ({ keyword: k, name: k, color: undefined, on: on.has(k) }));
  return [...defined, ...extra];
}

/**
 * 按输入的名字找到或新建标签, 返回关键字。名字 (不分大小写) 与已有标签或未定义的关键字相同时直接用那个。
 */
export async function ensureTag(name: string, marks: MessageMarks, known: string[] = []): Promise<string> {
  const v = name.trim().replace(/\s+/g, " ");
  const lower = v.toLowerCase();
  const existing = marks.tags.find((g) => g.name.toLowerCase() === lower || g.keyword === lower);
  if (existing) return existing.keyword;
  if (known.includes(lower) && /^[\x21-\x7e]+$/.test(lower)) return lower;
  const saved = await api.saveMessageTag({ keyword: "", name: v, color: nextTagColor(marks.tags) } as MessageTag);
  void marks.reload();
  return saved.keyword;
}
