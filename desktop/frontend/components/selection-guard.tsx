"use client";

import { useEffect } from "react";

import { installSelectionGuard } from "@/lib/app-selection";

/** 客户端里接管 Ctrl/⌘+A, 见 lib/app-selection.ts。 */
export function SelectionGuard() {
  useEffect(() => installSelectionGuard(), []);
  return null;
}
