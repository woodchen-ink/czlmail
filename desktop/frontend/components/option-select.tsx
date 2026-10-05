"use client";

import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { cn } from "@/lib/utils";

// Radix Select 把空字符串留给"未选择", 选项值不能为空; 内部换成占位值, 对外仍是空字符串。
const EMPTY = "__empty__";
const toItem = (v: string) => (v === "" ? EMPTY : v);
const fromItem = (v: string) => (v === EMPTY ? "" : v);

/**
 * 由 [值, 文字] 列表生成的下拉框, 替代原生 select:
 * macOS 的 WKWebView 会把原生 select 画成 Aqua 风格的凸起按钮, 且不继承字体。
 */
export function OptionSelect({
  value,
  onChange,
  options,
  placeholder,
  disabled,
  className,
  id,
  "aria-label": ariaLabel,
}: {
  value: string;
  onChange: (v: string) => void;
  options: readonly (readonly [string, string] | readonly string[])[];
  placeholder?: string;
  disabled?: boolean;
  className?: string;
  id?: string;
  "aria-label"?: string;
}) {
  // 没有空值选项时, 空字符串表示未选择, 显示 placeholder。
  const current = value ? value : options.some(([v]) => v === "") ? EMPTY : "";
  return (
    <Select value={current} onValueChange={(v) => onChange(fromItem(v))} disabled={disabled}>
      <SelectTrigger id={id} aria-label={ariaLabel} className={cn("min-w-0", className)}>
        <SelectValue placeholder={placeholder} />
      </SelectTrigger>
      <SelectContent>
        {options.map(([v, label]) => (
          <SelectItem key={v} value={toItem(v)}>
            {label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
