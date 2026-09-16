"use client";

import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";

import { cn } from "@/lib/utils";
import { api, errorMessage, type Account, type AppSettings } from "@/lib/api";
import { main } from "@/wailsjs/go/models";

export function SectionTitle({ title, desc, small }: { title: string; desc?: React.ReactNode; small?: boolean }) {
  return (
    <div>
      <h2 className={cn("font-semibold", small ? "text-sm" : "text-lg")}>{title}</h2>
      {desc && <p className="text-muted-foreground mt-1 text-sm leading-relaxed">{desc}</p>}
    </div>
  );
}

export function Card({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn("border-border bg-card flex flex-col gap-3 rounded-md border p-4", className)}>{children}</div>;
}

export function Divider() {
  return <div className="border-border border-t" />;
}

export function Row({ title, desc, children }: { title: React.ReactNode; desc?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-4">
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium">{title}</p>
        {desc && <p className="text-muted-foreground mt-0.5 text-xs leading-relaxed">{desc}</p>}
      </div>
      <div className="flex shrink-0 items-center gap-2">{children}</div>
    </div>
  );
}

export function useSettings(active: boolean) {
  const [settings, setSettings] = useState<AppSettings | null>(null);
  useEffect(() => {
    if (active)
      api
        .getSettings()
        .then(setSettings)
        .catch((err) => toast.error(errorMessage(err)));
  }, [active]);

  async function save(patch: Partial<AppSettings>) {
    if (!settings) return;
    const next = main.AppSettings.createFrom({ ...settings, ...patch });
    setSettings(next);
    try {
      await api.saveSettings(next);
    } catch (err) {
      setSettings(settings);
      toast.error(errorMessage(err));
    }
  }
  return { settings, save };
}

/** 设置页里按账号操作的部分(过滤器、自动回复、文件夹)用的账号选择。默认个人账号。 */
export function useAccounts(active: boolean) {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [accountId, setAccountId] = useState("");

  const load = useCallback(async () => {
    try {
      const list = (await api.listAccounts()) ?? [];
      setAccounts(list);
      setAccountId((cur) => (cur && list.some((a) => a.id === cur) ? cur : ((list.find((a) => a.isPersonal) ?? list[0])?.id ?? "")));
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }, []);

  useEffect(() => {
    if (active) load();
  }, [active, load]);

  return { accounts, accountId, setAccountId };
}

export function AccountPicker({
  accounts,
  value,
  onChange,
}: {
  accounts: Account[];
  value: string;
  onChange: (id: string) => void;
}) {
  if (accounts.length <= 1) return null;
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="border-input h-8 max-w-56 rounded-sm border bg-transparent px-2 text-sm"
      aria-label="账号"
    >
      {accounts.map((a) => (
        <option key={a.id} value={a.id} className="bg-popover">
          {a.name}
        </option>
      ))}
    </select>
  );
}

export function NativeSelect({
  value,
  onChange,
  options,
  className,
  disabled,
}: {
  value: string;
  onChange: (v: string) => void;
  options: [string, string][];
  className?: string;
  disabled?: boolean;
}) {
  return (
    <select
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value)}
      className={cn("border-input h-8 rounded-sm border bg-transparent px-2 text-sm disabled:opacity-50", className)}
    >
      {options.map(([v, label]) => (
        <option key={v} value={v} className="bg-popover">
          {label}
        </option>
      ))}
    </select>
  );
}
