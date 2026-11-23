"use client";

import { createContext, useContext, useState } from "react";
import { ChevronLeft, ChevronRight, Menu } from "lucide-react";
import { usePanelRef, type LayoutChangedMeta } from "react-resizable-panels";

import { useI18n } from "@/components/locale-provider";
import { Button } from "@/components/ui/button";
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "@/components/ui/resizable";
import { t } from "@/lib/i18n";
import { useCompact } from "@/lib/use-compact";
import { useBackAction } from "@/lib/native-back";
import { useEdgeSwipe } from "@/lib/use-edge-swipe";
import { cn } from "@/lib/utils";

/** 窄屏下当前显示的那一栏。 */
export type MobilePane = "sidebar" | "list" | "detail";

interface Props {
  /** 布局记忆的键，各模块互不影响。 */
  id: string;
  sidebar?: React.ReactNode;
  list?: React.ReactNode;
  detail: React.ReactNode;
  /** 专注阅读等场景下隐藏侧栏与列表。 */
  collapsed?: boolean;
  sidebarWidth?: number;
  listWidth?: number;
  /**
   * 窄屏下显示哪一栏。模块知道"点了一项就该看详情"时传入并在 onMobilePaneChange 里接住返回;
   * 不传时由本组件自己记, 顶部给出在各栏之间切换的按钮。
   */
  mobilePane?: MobilePane;
  onMobilePaneChange?: (pane: MobilePane) => void;
  /**
   * 窄屏下「返回」「菜单」按钮由模块自己放进工具栏 (经 useMobilePaneNav 取到), 本组件不再单起一行顶栏。
   * 省下一行高度, 按钮也和其它操作并排。
   */
  ownMobileNav?: boolean;
}

/** 窄屏下给模块工具栏用的导航动作; 宽屏 (多栏) 时为 null, 工具栏照旧不显示这两个按钮。 */
export interface MobilePaneNav {
  /** 回上一级 (详情 → 列表); 当前不在详情时为 null。 */
  back: (() => void) | null;
  /** 拉出侧栏抽屉; 没有侧栏或不在列表时为 null。 */
  openSidebar: (() => void) | null;
}

const MobilePaneNavContext = createContext<MobilePaneNav | null>(null);

export function useMobilePaneNav(): MobilePaneNav | null {
  return useContext(MobilePaneNavContext);
}

/** localStorage 在隐私窗口等环境下可能抛错，读写失败时只是不记忆布局。 */
const storage = {
  getItem(key: string) {
    try {
      return localStorage.getItem(key);
    } catch {
      return null;
    }
  },
  setItem(key: string, value: string) {
    try {
      localStorage.setItem(key, value);
    } catch {
      // 忽略
    }
  },
  removeItem(key: string) {
    try {
      localStorage.removeItem(key);
    } catch {
      // 忽略
    }
  },
};

function legacyLayoutKey(id: string, panelIds: string[]) {
  return `czlmail.layout.${id}:${panelIds.join(":")}`;
}

/** 侧栏与列表记住的宽度 (像素)。详情栏没有自己的宽度, 占剩下的部分。 */
type PaneWidths = { sidebar?: number; list?: number };

function readWidths(key: string): PaneWidths | null {
  const raw = storage.getItem(key);
  if (!raw) return null;
  try {
    const value: unknown = JSON.parse(raw);
    if (!value || typeof value !== "object") return null;
    const out: PaneWidths = {};
    for (const pane of ["sidebar", "list"] as const) {
      const px = (value as Record<string, unknown>)[pane];
      if (typeof px === "number" && Number.isFinite(px) && px > 0) out[pane] = Math.round(px);
    }
    return out.sidebar === undefined && out.list === undefined ? null : out;
  } catch {
    return null;
  }
}

/**
 * 旧版本按百分比存的布局 (react-resizable-panels 的 useDefaultLayout, 键为 `czlmail.layout.<模块>:<各栏 id>`)。
 * 只在还没有像素宽度时读一次, 让升级后的第一次启动仍是用户调好的样子, 随后即改存像素并删掉旧记录。
 */
function readLegacyLayout(id: string, panelIds: string[]): Record<string, number> | undefined {
  const raw = storage.getItem(legacyLayoutKey(id, panelIds));
  if (!raw) return undefined;
  try {
    const value: unknown = JSON.parse(raw);
    if (!value || typeof value !== "object") return undefined;
    const entries = Object.entries(value as Record<string, unknown>);
    if (entries.length !== panelIds.length || !entries.every(([, v]) => typeof v === "number")) return undefined;
    return value as Record<string, number>;
  } catch {
    return undefined;
  }
}

/**
 * 侧栏 / 列表 / 详情三栏，分隔线可拖动，宽度按模块记忆。
 *
 * 侧栏与列表按像素保持宽度（preserve-pixel-size）：拉伸窗口时只有详情栏变宽，
 * 否则侧栏会跟着窗口按比例变胖。
 *
 * 记忆也按像素：存百分比的话，下次启动窗口大小一变 (先以默认大小创建再最大化、换了显示器)，
 * 侧栏与列表就按比例变宽变窄，拖好的宽度每次启动都在漂。
 * 只记用户亲手拖出来的宽度：窗口缩小时库为了给详情栏腾地方会临时压窄侧栏，那不算数。
 */
export function PaneLayout(props: Props) {
  const compact = useCompact();
  return compact ? <CompactPanes {...props} /> : <WidePanes {...props} />;
}

function WidePanes({
  id,
  sidebar,
  list,
  detail,
  collapsed = false,
  sidebarWidth = 256,
  listWidth = 384,
}: Props) {
  const panelIds = collapsed
    ? ["detail"]
    : [sidebar ? "sidebar" : "", list ? "list" : "", "detail"].filter(Boolean);

  const key = `czlmail.panes.${id}`;
  // 每次渲染都读: 专注阅读时侧栏与列表会卸载, 回来重新挂载时要拿到期间保存的宽度。
  const saved = readWidths(key);
  const legacy = saved ? undefined : readLegacyLayout(id, panelIds);

  const sidebarPanel = usePanelRef();
  const listPanel = usePanelRef();

  const onLayoutChanged = (_layout: unknown, meta: LayoutChangedMeta) => {
    const current = readWidths(key);
    // 还没有像素记录时 (首次启动、刚从百分比升级) 也记一次当前宽度;
    // 窗口最小化时页面宽度不可信, 那时不记。
    if (!meta.isUserInteraction && (current || document.visibilityState !== "visible")) return;
    const next: PaneWidths = { ...current };
    const sidebarPx = sidebarPanel.current?.getSize().inPixels;
    const listPx = listPanel.current?.getSize().inPixels;
    if (sidebarPx) next.sidebar = Math.round(sidebarPx);
    if (listPx) next.list = Math.round(listPx);
    if (next.sidebar === undefined && next.list === undefined) return;
    storage.setItem(key, JSON.stringify(next));
    if (!current) storage.removeItem(legacyLayoutKey(id, panelIds));
  };

  return (
    <ResizablePanelGroup orientation="horizontal" defaultLayout={legacy} onLayoutChanged={onLayoutChanged}>
      {!collapsed && sidebar && (
        <>
          <ResizablePanel
            id="sidebar"
            panelRef={sidebarPanel}
            defaultSize={saved?.sidebar ?? sidebarWidth}
            minSize={180}
            maxSize={480}
            groupResizeBehavior="preserve-pixel-size"
            className="bg-sidebar flex flex-col"
          >
            {sidebar}
          </ResizablePanel>
          <ResizableHandle />
        </>
      )}
      {!collapsed && list && (
        <>
          <ResizablePanel
            id="list"
            panelRef={listPanel}
            defaultSize={saved?.list ?? listWidth}
            minSize={260}
            maxSize={720}
            groupResizeBehavior="preserve-pixel-size"
            className="flex flex-col"
          >
            {list}
          </ResizablePanel>
          <ResizableHandle />
        </>
      )}
      <ResizablePanel id="detail" minSize={320} className="min-w-0">
        {detail}
      </ResizablePanel>
    </ResizablePanelGroup>
  );
}

/**
 * 窄屏: 列表与详情一次只显示一个, 侧栏是从左侧滑出、盖在列表上的抽屉。
 * 三栏都保持挂载、只隐藏, 返回列表时滚动位置与加载的页不丢。
 * 左边缘右滑: 在详情里是返回, 在列表里是拉出抽屉。
 */
function CompactPanes({ sidebar, list, detail, mobilePane, onMobilePaneChange, ownMobileNav = false }: Props) {
  useI18n();
  const fallback: MobilePane = list ? "list" : "detail";
  const [own, setOwn] = useState<MobilePane>(fallback);
  const controlled = mobilePane !== undefined;
  const pane = (controlled ? mobilePane : own) ?? fallback;
  const go = (p: MobilePane) => (controlled ? onMobilePaneChange?.(p) : setOwn(p));

  // 没有列表的模块, 侧栏就是"列表": 抽屉盖在详情上。
  const base: MobilePane = list ? "list" : "detail";
  const drawerOpen = !!sidebar && pane === "sidebar";
  const shown: MobilePane = pane === "sidebar" ? base : pane;
  const back = shown === "detail" && list ? () => go("list") : null;
  const openSidebar = sidebar && !drawerOpen && shown === base ? () => go("sidebar") : null;

  const listSwipe = useEdgeSwipe(list ? openSidebar : null, false);
  const detailSwipe = useEdgeSwipe(back ?? (list ? null : openSidebar), !!back);
  const [drawerEl, setDrawerEl] = useState<HTMLDivElement | null>(null);
  useBackAction(drawerOpen ? () => go(base) : null, drawerEl);

  const showBar = !ownMobileNav && (back || openSidebar || (!controlled && shown === "list"));

  return (
    <MobilePaneNavContext.Provider value={{ back, openSidebar }}>
      <div className="relative flex h-full flex-col overflow-hidden">
        {showBar && (
          <div className="border-border flex h-11 shrink-0 items-center gap-1 border-b px-1">
            {back && (
              <Button variant="ghost" size="sm" onClick={back}>
                <ChevronLeft className="size-5" />
                {t("返回")}
              </Button>
            )}
            {openSidebar && (
              <Button variant="ghost" size="icon" onClick={openSidebar} aria-label={t("菜单")}>
                <Menu className="size-5" />
              </Button>
            )}
            {/* 模块没接管切换时, 列表里点了一项也不会自动进详情, 留一个入口。 */}
            {!controlled && shown === "list" && (
              <Button variant="ghost" size="sm" className="ml-auto" onClick={() => go("detail")}>
                {t("详情")}
                <ChevronRight className="size-5" />
              </Button>
            )}
          </div>
        )}
        <div className="relative min-h-0 flex-1">
          {list && (
            <div ref={listSwipe} className={cn("absolute inset-0 flex flex-col", shown !== "list" && "hidden")}>
              {list}
            </div>
          )}
          <div ref={detailSwipe} className={cn("bg-background absolute inset-0 shadow-lg", shown !== "detail" && "hidden")}>
            {detail}
          </div>
        </div>

        {sidebar && (
          <>
            <div
              className={cn(
                "absolute inset-0 z-30 bg-black/40 transition-opacity",
                drawerOpen ? "opacity-100" : "pointer-events-none opacity-0",
              )}
              onClick={() => go(base)}
              aria-hidden
            />
            <div
              ref={setDrawerEl}
              className={cn(
                "bg-sidebar border-border absolute inset-y-0 left-0 z-40 flex w-[85%] max-w-80 flex-col border-r shadow-xl transition-transform duration-200",
                drawerOpen ? "translate-x-0" : "-translate-x-full",
              )}
              inert={!drawerOpen}
            >
              {sidebar}
            </div>
          </>
        )}
      </div>
    </MobilePaneNavContext.Provider>
  );
}

/**
 * 放进模块工具栏的「返回」/「菜单」按钮, 只在窄屏且该动作可用时出现。
 * 要渲染在 PaneLayout 的栏内 (context 由它提供), 所以做成组件而不是让模块在外层调 hook。
 */
export function MobileNavButton({ kind, className }: { kind: "back" | "menu"; className?: string }) {
  useI18n();
  const nav = useMobilePaneNav();
  const action = kind === "back" ? nav?.back : nav?.openSidebar;
  if (!action) return null;
  return kind === "back" ? (
    <Button variant="ghost" size="icon" className={cn("size-8 shrink-0", className)} onClick={action} aria-label={t("返回")}>
      <ChevronLeft className="size-5" />
    </Button>
  ) : (
    <Button variant="ghost" size="icon" className={cn("size-8 shrink-0", className)} onClick={action} aria-label={t("菜单")}>
      <Menu className="size-5" />
    </Button>
  );
}
