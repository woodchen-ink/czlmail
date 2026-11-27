"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useState } from "react";
import { toast } from "sonner";
import { Server } from "lucide-react";

import { Card, SectionTitle } from "@/components/settings/ui";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { ACCOUNT_COLORS, accountColorValue, signInLookKey, useAccountLooksState, type AccountLooks } from "@/lib/account-looks";
import { api, errorMessage, type Account } from "@/lib/api";
import { cn } from "@/lib/utils";
import { main } from "@/wailsjs/go/models";

/**
 * 设置 → 账户 → 可访问的邮箱: 每个邮箱一行, 左边的圆点挑颜色, 右边起名字、决定共享邮箱是否并入「全部账号」。
 * 多个登录时按登录分组 (不同登录里可能有同名邮箱), 附加登录的组头可以给它在侧栏里的那一节起标题。
 * 名字与颜色经个人网盘同步到别的设备, 服务器上的账号名不变。
 */
export function MailboxesCard({
  accounts,
  excluded,
  onToggleUnified,
  disabled,
}: {
  accounts: Account[];
  /** 不并入「全部账号」的共享邮箱。 */
  excluded: Set<string>;
  onToggleUnified: (accountId: string, on: boolean) => void;
  disabled: boolean;
}) {
  useI18n();
  const { looks, reload } = useAccountLooksState();
  // 保存并重读完之前先按刚改的显示, 免得颜色点了要等一拍才变。
  const [pending, setPending] = useState<AccountLooks>({});
  const shown = { ...looks, ...pending };

  async function update(key: string, patch: { name?: string; color?: string }) {
    const next = main.AccountLook.createFrom({ ...shown[key], ...patch });
    setPending((p) => ({ ...p, [key]: next }));
    try {
      await api.setAccountLook(key, next);
      await reload();
    } catch (err) {
      toast.error(errorMessage(err));
    }
    setPending((p) => {
      const rest = { ...p };
      delete rest[key];
      return rest;
    });
  }

  const groups = [...new Set(accounts.map((a) => a.signIn))].map((signIn) => {
    const list = accounts.filter((a) => a.signIn === signIn);
    return { signIn, host: list[0].signInHost || signIn, list };
  });
  const grouped = groups.length > 1;

  return (
    <>
      <SectionTitle
        title={t("可访问的邮箱")}
        desc={
          <>
            {t("由服务器按你的权限共享，在邮件侧栏的「共享」下显示，新共享的邮箱会自动出现。开关决定是否并入「全部账号」；关闭后仍可在「共享」下单独查看，统一搜索照常包含它。")}
            {t("圆点与名称是你给邮箱挑的颜色和起的名字，用在侧栏与「全部账号」里，经个人网盘同步到你的其它设备。")}
          </>
        }
        small
      />
      <Card className="gap-0 p-0">
        {groups.map((g, gi) => (
          <div key={g.signIn} className={cn(gi > 0 && "border-border border-t")}>
            {grouped && (
              <div className="text-muted-foreground flex items-center gap-2 px-4 pt-2.5 pb-1 text-xs">
                <Server className="size-3.5 shrink-0" />
                <span className="min-w-0 flex-1 truncate">{g.host}</span>
                {/* 附加登录各占侧栏一节, 标题默认是服务器地址; 主登录那节叫「文件夹」, 不用起名。 */}
                {g.signIn && (
                  <NameInput
                    value={shown[signInLookKey(g.signIn)]?.name ?? ""}
                    placeholder={t("侧栏标题")}
                    onCommit={(name) => update(signInLookKey(g.signIn), { name })}
                  />
                )}
                <span aria-hidden className="w-[4.75rem] shrink-0" />
              </div>
            )}
            {g.list.map((a, i) => (
              <div
                key={a.id}
                className={cn(
                  "flex items-center gap-3 py-2 pr-4 pl-3 text-sm",
                  (i > 0 || grouped) && "border-border border-t",
                )}
              >
                <ColorPicker value={shown[a.id]?.color ?? ""} onChange={(color) => update(a.id, { color })} />
                <span className="min-w-0 flex-1 truncate" title={a.name}>
                  {a.name}
                </span>
                <NameInput
                  value={shown[a.id]?.name ?? ""}
                  placeholder={t("显示名称")}
                  onCommit={(name) => update(a.id, { name })}
                />
                <span className="text-muted-foreground w-7 shrink-0 text-xs">{a.isPersonal ? t("个人") : t("共享")}</span>
                {a.isPersonal ? (
                  <span aria-hidden className="w-8 shrink-0" />
                ) : (
                  <Switch
                    aria-label={t("并入全部账号")}
                    checked={!excluded.has(a.id)}
                    disabled={disabled}
                    onCheckedChange={(on) => onToggleUnified(a.id, on)}
                  />
                )}
              </div>
            ))}
          </div>
        ))}
      </Card>
    </>
  );
}

/** 失焦或回车保存, Esc 放弃; 外面的值变了 (保存完、别的设备同步来) 时草稿跟着换。 */
function NameInput({
  value,
  placeholder,
  onCommit,
}: {
  value: string;
  placeholder: string;
  onCommit: (name: string) => void;
}) {
  const [draft, setDraft] = useState(value);
  const [seen, setSeen] = useState(value);
  if (value !== seen) {
    setSeen(value);
    setDraft(value);
  }
  return (
    <Input
      value={draft}
      placeholder={placeholder}
      aria-label={placeholder}
      className="h-7 w-44 shrink-0 text-sm"
      onChange={(e) => setDraft(e.target.value)}
      onBlur={() => {
        if (draft.trim() !== value) onCommit(draft.trim());
      }}
      onKeyDown={(e) => {
        if (e.key === "Enter") e.currentTarget.blur();
        if (e.key === "Escape") setDraft(value);
      }}
    />
  );
}

function ColorPicker({ value, onChange }: { value: string; onChange: (key: string) => void }) {
  useI18n();
  const [open, setOpen] = useState(false);
  const current = accountColorValue(value);
  const pick = (key: string) => {
    setOpen(false);
    if (key !== value) onChange(key);
  };
  const swatches = [{ key: "", value: undefined, label: () => t("无颜色") }, ...ACCOUNT_COLORS];
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={t("颜色")}
          title={t("颜色")}
          className="hover:bg-secondary flex size-6 shrink-0 items-center justify-center rounded-full"
        >
          <Swatch color={current} />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-auto flex-row gap-1 p-1.5">
        {swatches.map((c) => (
          <button
            key={c.key}
            type="button"
            onClick={() => pick(c.key)}
            aria-label={c.label()}
            title={c.label()}
            aria-pressed={value === c.key}
            className={cn(
              "hover:bg-secondary flex size-7 items-center justify-center rounded-full",
              value === c.key && "bg-secondary",
            )}
          >
            <Swatch color={c.value} selected={value === c.key} />
          </button>
        ))}
      </PopoverContent>
    </Popover>
  );
}

/** 色块: 没颜色时是一个空心圆。选中时外圈一道细环。 */
function Swatch({ color, selected }: { color?: string; selected?: boolean }) {
  return (
    <span
      aria-hidden
      className={cn(
        "size-3.5 rounded-full",
        !color && "border-muted-foreground/50 border border-dashed",
        selected && "ring-foreground/30 ring-offset-background ring-1 ring-offset-2",
      )}
      style={color ? { backgroundColor: color } : undefined}
    />
  );
}
