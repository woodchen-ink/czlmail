"use client";

import { useCallback, useRef, useState } from "react";
import { toast } from "sonner";
import { errorMessage } from "@/lib/api";
import { t } from "@/lib/i18n";
import { noteApi } from "@/lib/notes/api";

/** Network synchronization has its own state and never owns the local editor action lock. */
export function useNoteSync(flush: () => Promise<void>) {
  const [syncing, setSyncing] = useState(false);
  const pending = useRef<Promise<void> | null>(null);

  /**
   * Persist the current draft first; repeated requests share the same in-flight operation.
   * With announce (the sync button) the outcome is reported; the backend waits for a pass already
   * running, so "synced" really means a full pass finished.
   */
  const sync = useCallback((announce = false) => {
    if (pending.current) return pending.current;
    setSyncing(true);
    const task = Promise.resolve().then(flush).then(() => noteApi.sync())
      .then((left) => {
        if (!announce) return;
        if (left > 0) toast.warning(t("{0} 项未能同步", left)); else toast.success(t("已同步"));
      })
      .catch((err) => { toast.error(errorMessage(err)); })
      .finally(() => { pending.current = null; setSyncing(false); });
    pending.current = task;
    return task;
  }, [flush]);

  return { syncing, sync };
}
