"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Building2, X } from "lucide-react";

import { SenderAvatar } from "@/components/sender-avatar";
import { cn } from "@/lib/utils";
import { api, type Recipient } from "@/lib/api";

interface Props {
  /** 逗号分隔的地址串, 例如 `张三 <a@x.com>, b@y.com`。与写信、日程参加者等调用方保持同一格式。 */
  value: string;
  onChange: (value: string) => void;
  placeholder: string;
  label: string;
  /** 嵌在写信表头里时不画边框。 */
  bare?: boolean;
}

interface Chip {
  name: string;
  email: string;
}

const SEPARATORS = /[,;，；\n]/;
const EMAIL_RE = /^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/;

function parseOne(raw: string): Chip | null {
  const s = raw.trim();
  if (!s) return null;
  const m = s.match(/^(.*?)\s*<([^>]+)>$/);
  if (m) return { name: m[1].replace(/^["']|["']$/g, "").trim(), email: m[2].trim() };
  return { name: "", email: s };
}

function format(c: Chip): string {
  return c.name && c.name !== c.email ? `${c.name.replace(/[<>,;"]/g, "")} <${c.email}>` : c.email;
}

type Suggestion = Recipient & { directory?: boolean };

/**
 * 收件人输入: 已确认的地址显示为标签, 回车 / Tab / 逗号 / 分号确认当前输入, 退格删除上一个。
 * 候选来自通讯录、最近往来的地址与同邮局用户目录。
 */
export function RecipientInput({ value, onChange, placeholder, label, bare }: Props) {
  const [text, setText] = useState("");
  const [suggestions, setSuggestions] = useState<Suggestion[]>([]);
  const [active, setActive] = useState(0);
  const [focused, setFocused] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  // 父组件的值里末尾可能带着正在输入的文字(随打随报, 保证点发送时不丢), 解析标签时剔除。
  const chips = useMemo(() => {
    const parts = value.split(SEPARATORS);
    if (text.trim() && parts.length > 0 && parts[parts.length - 1].trim() === text.trim()) parts.pop();
    return parts.map(parseOne).filter((c): c is Chip => c !== null);
  }, [value, text]);

  function emit(nextChips: Chip[], nextText: string) {
    const all = nextChips.map(format);
    if (nextText.trim()) all.push(nextText.trim());
    onChange(all.join(", "));
  }

  function commit(raw: string) {
    const added = raw
      .split(SEPARATORS)
      .map(parseOne)
      .filter((c): c is Chip => c !== null)
      .filter((c) => !chips.some((x) => x.email.toLowerCase() === c.email.toLowerCase()));
    setText("");
    setSuggestions([]);
    emit([...chips, ...added], "");
  }

  function pick(s: Suggestion) {
    setText("");
    setSuggestions([]);
    if (!chips.some((x) => x.email.toLowerCase() === s.email.toLowerCase())) {
      emit([...chips, { name: s.name, email: s.email }], "");
    } else {
      emit(chips, "");
    }
    inputRef.current?.focus();
  }

  function remove(i: number) {
    emit(
      chips.filter((_, j) => j !== i),
      text,
    );
  }

  const term = text.trim();
  useEffect(() => {
    if (!focused || term.length < 1) {
      setSuggestions([]);
      return;
    }
    let cancelled = false;
    const t = setTimeout(async () => {
      const [local, dir] = await Promise.all([
        api.searchRecipients(term).catch(() => []),
        api.searchDirectory(term).catch(() => []),
      ]);
      if (cancelled) return;
      const used = new Set(chips.map((c) => c.email.toLowerCase()));
      const seen = new Set<string>();
      const merged: Suggestion[] = [];
      // 同邮局用户排在前面: 多数时候是给同事写信, 目录里的姓名也比往来记录里的更准。
      for (const r of [...(dir ?? []).map((d) => ({ ...d, directory: true })), ...(local ?? [])]) {
        const key = r.email.toLowerCase();
        if (used.has(key) || seen.has(key)) continue;
        seen.add(key);
        merged.push(r);
      }
      setSuggestions(merged.slice(0, 12));
      setActive(0);
    }, 120);
    return () => {
      cancelled = true;
      clearTimeout(t);
    };
    // chips 变化(确认了一个地址)时 term 也会清空, 不必单独列入。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [term, focused]);

  const show = focused && suggestions.length > 0;

  return (
    <div className="relative min-w-0 flex-1">
      <div
        onMouseDown={(e) => {
          if (e.target === e.currentTarget) {
            e.preventDefault();
            inputRef.current?.focus();
          }
        }}
        className={cn(
          "flex min-h-9 min-w-0 flex-wrap items-center gap-1 py-1",
          !bare && "border-input focus-within:ring-ring/50 rounded-sm border px-2 focus-within:ring-2",
        )}
      >
        {chips.map((c, i) => {
          const valid = EMAIL_RE.test(c.email);
          return (
            <span
              key={`${c.email}-${i}`}
              title={format(c)}
              onDoubleClick={() => {
                // 双击改回文字编辑。
                const rest = chips.filter((_, j) => j !== i);
                setText(format(c));
                emit(rest, format(c));
                inputRef.current?.focus();
              }}
              className={cn(
                "flex max-w-64 items-center gap-1 rounded-sm border py-0.5 pr-0.5 pl-2 text-sm",
                valid ? "border-border bg-secondary" : "border-destructive text-destructive",
              )}
            >
              <span className="truncate">{c.name ? `${c.name} (${c.email})` : c.email}</span>
              <button
                type="button"
                aria-label={`移除 ${c.email}`}
                className="text-muted-foreground hover:text-foreground rounded-sm p-0.5"
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => remove(i)}
              >
                <X className="size-3" />
              </button>
            </span>
          );
        })}
        <input
          ref={inputRef}
          value={text}
          onChange={(e) => {
            const v = e.target.value;
            // 输入分隔符即确认前面的地址。
            if (SEPARATORS.test(v)) {
              commit(v);
              return;
            }
            setText(v);
            emit(chips, v);
          }}
          onPaste={(e) => {
            const pasted = e.clipboardData.getData("text");
            if (SEPARATORS.test(pasted)) {
              e.preventDefault();
              commit(text + pasted);
            }
          }}
          onFocus={() => setFocused(true)}
          onBlur={() => {
            setFocused(false);
            if (text.trim()) commit(text);
          }}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown" && show) {
              e.preventDefault();
              setActive((i) => (i + 1) % suggestions.length);
            } else if (e.key === "ArrowUp" && show) {
              e.preventDefault();
              setActive((i) => (i - 1 + suggestions.length) % suggestions.length);
            } else if (e.key === "Enter" || (e.key === "Tab" && term)) {
              if (!term) return;
              e.preventDefault();
              const s = suggestions[active];
              // 已经敲完整的地址按原样确认, 除非高亮的候选就是这个地址(带上姓名)。
              if (show && s && (!EMAIL_RE.test(term) || s.email.toLowerCase() === term.toLowerCase())) pick(s);
              else commit(term);
            } else if (e.key === "Backspace" && !text && chips.length > 0) {
              e.preventDefault();
              remove(chips.length - 1);
            } else if (e.key === "Escape" && show) {
              e.stopPropagation();
              setSuggestions([]);
            }
          }}
          placeholder={chips.length === 0 ? placeholder : ""}
          aria-label={label}
          aria-autocomplete="list"
          aria-expanded={show}
          autoComplete="off"
          className="placeholder:text-muted-foreground h-7 min-w-32 flex-1 bg-transparent text-sm outline-none"
        />
      </div>

      {show && (
        <ul
          role="listbox"
          className="border-border bg-popover text-popover-foreground absolute inset-x-0 top-full z-50 mt-1 max-h-72 overflow-y-auto rounded-md border p-1 shadow-md"
        >
          {suggestions.map((r, i) => (
            <li
              key={r.email}
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
              <span className="min-w-0 flex-1 truncate text-sm">
                {r.name && r.name !== r.email ? (
                  <>
                    {r.name} <span className="text-muted-foreground">&lt;{r.email}&gt;</span>
                  </>
                ) : (
                  r.email
                )}
              </span>
              {r.directory && (
                <span className="text-muted-foreground flex shrink-0 items-center gap-1 text-xs">
                  <Building2 className="size-3" />
                  同邮局
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
