"use client";

import { useEffect, useState } from "react";
import { ChevronsUpDown, Image as ImageIcon, Keyboard, LogOut, Monitor, Moon, Sun, Upload } from "lucide-react";
import { toast } from "sonner";

import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { SenderAvatar } from "@/components/sender-avatar";
import { api, errorMessage } from "@/lib/api";
import { useAppearance, type ThemePreference } from "@/lib/theme";
import { main } from "@/wailsjs/go/models";

interface Props {
  username: string;
  onImport: () => void;
  onShortcuts: () => void;
  onSignOut: () => void;
}

export function UserMenu({ username, onImport, onShortcuts, onSignOut }: Props) {
  const { theme, setTheme, bodyAppearance, setBodyAppearance } = useAppearance();
  const [avatars, setAvatars] = useState(true);

  useEffect(() => {
    api
      .getSettings()
      .then((s) => setAvatars(s.senderAvatars))
      .catch(() => {});
  }, []);

  async function toggleAvatars(on: boolean) {
    setAvatars(on);
    try {
      const current = await api.getSettings();
      await api.saveSettings(main.AppSettings.createFrom({ ...current, senderAvatars: on }));
      toast.success(on ? "已开启发件人头像，新加载的列表生效" : "已关闭发件人头像");
    } catch (err) {
      setAvatars(!on);
      toast.error(errorMessage(err));
    }
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger className="hover:bg-secondary focus-visible:ring-ring flex w-full items-center gap-2 px-3 py-3 text-left focus-visible:ring-2 focus-visible:outline-none">
        <SenderAvatar email={username} className="size-7" />
        <span className="min-w-0 flex-1 truncate text-sm font-medium">{username || "未登录"}</span>
        <ChevronsUpDown className="text-muted-foreground size-4 shrink-0" />
      </DropdownMenuTrigger>

      <DropdownMenuContent align="start" className="w-60">
        <DropdownMenuLabel className="truncate">{username}</DropdownMenuLabel>
        <DropdownMenuSeparator />

        <DropdownMenuSub>
          <DropdownMenuSubTrigger>
            <Sun className="size-4" />
            外观
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent>
            <DropdownMenuRadioGroup value={theme} onValueChange={(v) => setTheme(v as ThemePreference)}>
              <DropdownMenuRadioItem value="system">
                <Monitor className="size-4" />
                跟随系统
              </DropdownMenuRadioItem>
              <DropdownMenuRadioItem value="light">
                <Sun className="size-4" />
                亮色
              </DropdownMenuRadioItem>
              <DropdownMenuRadioItem value="dark">
                <Moon className="size-4" />
                暗色
              </DropdownMenuRadioItem>
            </DropdownMenuRadioGroup>
            <DropdownMenuSeparator />
            <DropdownMenuCheckboxItem
              checked={bodyAppearance === "light"}
              onCheckedChange={(on) => setBodyAppearance(on ? "light" : "follow")}
            >
              邮件正文始终亮色
            </DropdownMenuCheckboxItem>
          </DropdownMenuSubContent>
        </DropdownMenuSub>

        <DropdownMenuCheckboxItem checked={avatars} onCheckedChange={toggleAvatars}>
          <ImageIcon className="size-4" />
          显示发件人头像
        </DropdownMenuCheckboxItem>

        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={onImport}>
          <Upload className="size-4" />
          导入邮件到当前文件夹
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={onShortcuts}>
          <Keyboard className="size-4" />
          键盘快捷键
        </DropdownMenuItem>

        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={onSignOut}>
          <LogOut className="size-4" />
          退出登录
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
