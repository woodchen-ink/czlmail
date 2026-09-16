"use client";

import { useDefaultLayout } from "react-resizable-panels";

import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "@/components/ui/resizable";

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
};

/**
 * 侧栏 / 列表 / 详情三栏，分隔线可拖动，宽度按模块记忆。
 *
 * 侧栏与列表按像素保持宽度（preserve-pixel-size）：拉伸窗口时只有详情栏变宽，
 * 否则侧栏会跟着窗口按比例变胖。
 */
export function PaneLayout({
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

  const { defaultLayout, onLayoutChanged } = useDefaultLayout({
    id: `czlmail.layout.${id}`,
    panelIds,
    storage,
  });

  return (
    <ResizablePanelGroup orientation="horizontal" defaultLayout={defaultLayout} onLayoutChanged={onLayoutChanged}>
      {!collapsed && sidebar && (
        <>
          <ResizablePanel
            id="sidebar"
            defaultSize={sidebarWidth}
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
            defaultSize={listWidth}
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
