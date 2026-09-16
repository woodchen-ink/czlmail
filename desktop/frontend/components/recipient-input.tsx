"use client";

import { useEffect, useRef, useState } from "react";

import { Input } from "@/components/ui/input";
import { SenderAvatar } from "@/components/sender-avatar";
import { cn } from "@/lib/utils";
import { api, type Recipient } from "@/lib/api";

interface Props {
  value: string;
  onChange: (value: string) => void;
  placeholder: string;
  label: string;
}

/**
 * 收件人输入框：逗号分隔的地址串，最后一段正在输入的内容从通讯录补全。
 *
 * 保持纯文本而不做成标签式输入：粘贴一串地址、手动修改其中一个字母都更直接。
 */
export function RecipientInput({ value, onChange, placeholder, label }: Props) {
  const [suggestions, setSuggestions] = useState<Recipient[]>([]);
  const [active, setActive] = useState(0);
  const [open, setOpen] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  const term = currentTerm(value);

  useEffect(() => {
    if (!open || term.length < 1) {
      setSuggestions([]);
      return;
    }
    let cancelled = false;
    const t = setTimeout(() => {
      api
        .searchRecipients(term)
        .then((list) => {
          if (cancelled) return;
          // 已经填过的地址不再推荐。
          const used = new Set(value.toLowerCase().split(/[,;，；]/).map((s) => s.trim()));
          setSuggestions((list ?? []).filter((r) => ![...used].some((u) => u.includes(r.email.toLowerCase()))));
          setActive(0);
        })
        .catch(() => setSuggestions([]));
    }, 120);
    return () => {
      cancelled = true;
      clearTimeout(t);
    };
  }, [term, open, value]);

  function pick(r: Recipient) {
    const parts = value.split(/[,;，；]/);
    parts[parts.length - 1] = r.name && r.name !== r.email ? ` ${r.name} <${r.email}>` : ` ${r.email}`;
    onChange(parts.join(",").replace(/^\s+/, "") + ", ");
    setSuggestions([]);
    inputRef.current?.focus();
  }

  const show = open && suggestions.length > 0;

  return (
    <div className="relative">
      <Input
        ref={inputRef}
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        onBlur={() => setTimeout(() => setOpen(false), 120)}
        onKeyDown={(e) => {
          if (!show) return;
          if (e.key === "ArrowDown") {
            e.preventDefault();
            setActive((i) => (i + 1) % suggestions.length);
          } else if (e.key === "ArrowUp") {
            e.preventDefault();
            setActive((i) => (i - 1 + suggestions.length) % suggestions.length);
          } else if (e.key === "Enter" || e.key === "Tab") {
            e.preventDefault();
            pick(suggestions[active]);
          } else if (e.key === "Escape") {
            e.stopPropagation();
            setSuggestions([]);
          }
        }}
        placeholder={placeholder}
        aria-label={label}
        aria-autocomplete="list"
        aria-expanded={show}
        autoComplete="off"
      />
      {show && (
        <ul
          role="listbox"
          className="border-border bg-popover text-popover-foreground absolute inset-x-0 top-full z-50 mt-1 max-h-64 overflow-y-auto rounded-md border p-1 shadow-md"
        >
          {suggestions.map((r, i) => (
            <li
              key={`${r.email}/${r.name}`}
              role="option"
              aria-selected={i === active}
              onMouseDown={(e) => {
                e.preventDefault();
                pick(r);
              }}
              onMouseEnter={() => setActive(i)}
              className={cn("flex cursor-default items-center gap-2 rounded-sm px-2 py-1.5", i === active && "bg-secondary")}
            >
              <SenderAvatar email={r.email} name={r.name} className="size-6 text-[10px]" />
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm">{r.name || r.email}</span>
                {r.name && r.name !== r.email && <span className="text-muted-foreground block truncate text-xs">{r.email}</span>}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function currentTerm(value: string): string {
  const parts = value.split(/[,;，；]/);
  return (parts[parts.length - 1] ?? "").trim();
}
