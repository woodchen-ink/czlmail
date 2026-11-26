"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useState } from "react";
import { Ban, Check, Server, UserRound } from "lucide-react";

import { Card, SectionTitle } from "@/components/settings/ui";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { ACCOUNT_COLORS, accountColorValue, signInLookKey, type AccountLooks } from "@/lib/account-looks";
import { cn } from "@/lib/utils";
import { main } from "@/wailsjs/go/models";
import type { Account, AppSettings } from "@/lib/api";

/**
 * 设置 → 账户 → 名称与颜色: 给账号 (和其它登录在侧栏里的那一节) 起个本机的名字、挑个颜色 (同 Thunderbird)。
 * 名字用在侧栏、统一视图的邮件标记与写信的发件人里; 颜色是侧栏的小圆点与统一视图里邮件行左边的竖线。
 * 只存本机, 服务器上的账号名不变。
 */
export function AccountLooksCard({
  accounts,
  settings,
  save,
}: {
  accounts: Account[];
  settings: AppSettings | null;
  save: (patch: Partial<AppSettings>) => Promise<void>;
}) {
  useI18n();
  const looks: AccountLooks = settings?.accountLooks ?? {};

  function update(key: string, patch: { name?: string; color?: string }) {
    const cur = looks[key] ?? { name: "", color: "" };
    const next = main.AccountLook.createFrom({ ...cur, ...patch });
    const all = { ...looks, [key]: next };
    if (!next.name && !next.color) delete all[key];
    void save({ accountLooks: all });
  }

  // 其它登录各占侧栏一节, 标题默认是服务器地址; 主登录的那节叫「文件夹」, 不用起名。
  const signIns = [...new Set(accounts.filter((a) => a.signIn).map((a) => a.signIn))].map((id) => ({
    id,
    host: accounts.find((a) => a.signIn === id)?.signInHost || id,
  }));

  return (
    <>
      <SectionTitle
        title={t("名称与颜色")}
        desc={t("只保存在本机。名称替换侧栏和统一视图里服务器给的名字；颜色显示为侧栏里的圆点和「全部账号」里邮件左侧的竖线。留空则保持默认。")}
        small
      />
      <Card className="gap-0 p-0">
        {signIns.map((s, i) => (
          <LookRow
            key={`signin:${s.id}:${looks[signInLookKey(s.id)]?.name ?? ""}`}
            divider={i > 0}
            icon={<Server className="text-muted-foreground size-4 shrink-0" />}
            title={t("登录 {0}", s.host)}
            placeholder={s.host}
            name={looks[signInLookKey(s.id)]?.name ?? ""}
            onName={(name) => update(signInLookKey(s.id), { name })}
            disabled={!settings}
          />
        ))}
        {accounts.map((a, i) => (
          <LookRow
            key={`${a.id}:${looks[a.id]?.name ?? ""}`}
            divider={i > 0 || signIns.length > 0}
            icon={<UserRound className="text-muted-foreground size-4 shrink-0" />}
            title={a.name}
            placeholder={a.name}
            name={looks[a.id]?.name ?? ""}
            onName={(name) => update(a.id, { name })}
            color={looks[a.id]?.color ?? ""}
            onColor={(color) => update(a.id, { color })}
            disabled={!settings}
          />
        ))}
      </Card>
    </>
  );
}

function LookRow({
  divider,
  icon,
  title,
  placeholder,
  name,
  onName,
  color,
  onColor,
  disabled,
}: {
  divider: boolean;
  icon: React.ReactNode;
  title: string;
  placeholder: string;
  name: string;
  onName: (name: string) => void;
  color?: string;
  onColor?: (color: string) => void;
  disabled: boolean;
}) {
  useI18n();
  // 保存后的名字变了时外面按新 key 重建这一行, 草稿随之重置。
  const [draft, setDraft] = useState(name);
  const commit = () => {
    if (draft.trim() !== name) onName(draft.trim());
  };

  return (
    <div className={cn("flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-2.5 text-sm", divider && "border-border border-t")}>
      <div className="flex min-w-0 flex-1 basis-40 items-center gap-2">
        {icon}
        <span className="truncate" title={title}>
          {title}
        </span>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        <Input
          value={draft}
          placeholder={placeholder}
          aria-label={t("显示名称")}
          disabled={disabled}
          className="h-8 w-52"
          onChange={(e) => setDraft(e.target.value)}
          onBlur={commit}
          onKeyDown={(e) => {
            if (e.key === "Enter") (e.target as HTMLInputElement).blur();
            if (e.key === "Escape") setDraft(name);
          }}
        />
        {onColor ? (
          <ColorPicker value={color ?? ""} onChange={onColor} disabled={disabled} />
        ) : (
          <span aria-hidden className="size-8" />
        )}
      </div>
    </div>
  );
}

function ColorPicker({ value, onChange, disabled }: { value: string; onChange: (key: string) => void; disabled: boolean }) {
  useI18n();
  const [open, setOpen] = useState(false);
  const current = accountColorValue(value);
  const pick = (key: string) => {
    setOpen(false);
    if (key !== value) onChange(key);
  };
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          disabled={disabled}
          aria-label={t("颜色")}
          title={t("颜色")}
          className="border-border hover:bg-secondary flex size-8 shrink-0 items-center justify-center rounded-md border disabled:opacity-50"
        >
          {current ? (
            <span className="size-4 rounded-full" style={{ backgroundColor: current }} />
          ) : (
            <Ban className="text-muted-foreground size-4" />
          )}
        </button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-auto p-2">
        <div className="grid grid-cols-5 gap-1.5">
          <button
            type="button"
            onClick={() => pick("")}
            aria-label={t("无颜色")}
            title={t("无颜色")}
            className="border-border hover:bg-secondary flex size-7 items-center justify-center rounded-full border"
          >
            <Ban className="text-muted-foreground size-3.5" />
          </button>
          {ACCOUNT_COLORS.map((c) => (
            <button
              key={c.key}
              type="button"
              onClick={() => pick(c.key)}
              aria-label={c.label()}
              title={c.label()}
              className="flex size-7 items-center justify-center rounded-full ring-offset-2 hover:ring-2 hover:ring-current"
              style={{ backgroundColor: c.value, color: c.value }}
            >
              {value === c.key && <Check className="size-3.5 text-white" />}
            </button>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  );
}
