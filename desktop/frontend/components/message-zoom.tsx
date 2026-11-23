"use client";

import { useState } from "react";
import { Minus, Plus } from "lucide-react";

import { Button } from "@/components/ui/button";
import { t } from "@/lib/i18n";

/** 正文手动缩放的档位, 与浏览器一致。 */
export const ZOOM_STEPS = [0.5, 0.67, 0.75, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3];

/** step: 1 放大一档, -1 缩小一档, 0 回到 100%。 */
export function nextZoom(current: number, step: number): number {
  if (step === 0) return 1;
  if (step > 0) return ZOOM_STEPS.find((z) => z > current + 0.001) ?? current;
  return [...ZOOM_STEPS].reverse().find((z) => z < current - 0.001) ?? current;
}

/** Ctrl+加号/等号放大, Ctrl+减号缩小, Ctrl+0 还原; 其余返回 null。 */
export function zoomStepOf(e: KeyboardEvent): number | null {
  if (!(e.ctrlKey || e.metaKey) || e.altKey) return null;
  if (e.key === "+" || e.key === "=") return 1;
  if (e.key === "-") return -1;
  if (e.key === "0" || e.code === "Digit0" || e.code === "Numpad0") return 0;
  return null;
}

/**
 * 一封邮件的手动缩放: key(邮件 id)变了就回到 100%。
 * 翻译只换正文不换 key, 缩放保留。
 */
export function useMessageZoom(key: string): [number, (step: number) => void] {
  const [zoom, setZoom] = useState(1);
  const [zoomFor, setZoomFor] = useState(key);
  if (zoomFor !== key) {
    setZoomFor(key);
    setZoom(1);
  }
  return [zoom, (step: number) => setZoom((z) => nextZoom(z, step))];
}

/** 缩放指示: − 百分比 +, 点百分比回到 100%。放在正文上方的工具行里, 不压在正文上。 */
export function MessageZoomControl({ zoom, onStep }: { zoom: number; onStep: (step: number) => void }) {
  return (
    <div className="border-border text-foreground flex h-7 items-center rounded-md border">
      <Button size="icon" variant="ghost" className="size-6.5" title={t("缩小")} aria-label={t("缩小")}
        disabled={zoom <= ZOOM_STEPS[0]} onClick={() => onStep(-1)}>
        <Minus className="size-3.5" />
      </Button>
      <button type="button" className="hover:bg-accent h-full min-w-11 px-1 text-xs tabular-nums"
        title={t("恢复原始大小")} onClick={() => onStep(0)}>
        {Math.round(zoom * 100)} %
      </button>
      <Button size="icon" variant="ghost" className="size-6.5" title={t("放大")} aria-label={t("放大")}
        disabled={zoom >= ZOOM_STEPS[ZOOM_STEPS.length - 1]} onClick={() => onStep(1)}>
        <Plus className="size-3.5" />
      </Button>
    </div>
  );
}
