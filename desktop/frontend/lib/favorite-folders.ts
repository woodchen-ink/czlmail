"use client";

import { useEffect, useState } from "react";

import { Events, api, onEvent } from "@/lib/api";
import type { main } from "@/wailsjs/go/models";

/** 侧栏「收藏」里的文件夹, 按收藏的先后; 经个人网盘的 account-settings.json 同步到别的设备。 */
export type FavoriteFolder = main.FavoriteFolder;

/** 读收藏的文件夹; 本机修改与别的设备同步过来时 (都发 settings:changed) 跟着更新。 */
export function useFavoriteFolders(): FavoriteFolder[] {
  const [list, setList] = useState<FavoriteFolder[]>([]);
  useEffect(() => {
    const load = () =>
      api
        .getFavoriteFolders()
        .then((l) => setList(l ?? []))
        .catch(() => {});
    load();
    const offSettings = onEvent(Events.settingsChanged, load);
    // 登录增减时本机账号 id 与服务器键的对应跟着变。
    const offAccounts = onEvent(Events.accountsChanged, load);
    return () => {
      offSettings();
      offAccounts();
    };
  }, []);
  return list;
}
