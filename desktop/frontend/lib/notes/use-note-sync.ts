"use client";

import { useCallback, useRef, useState } from "react";
import { toast } from "sonner";
import { errorMessage } from "@/lib/api";
import { noteApi } from "@/lib/notes/api";

/** Network synchronization has its own state and never owns the local editor action lock. */
export function useNoteSync(flush: () => Promise<void>) {
  const [syncing, setSyncing] = useState(false);
  const pending = useRef<Promise<void> | null>(null);

  /** Persist the current draft first; repeated requests share the same in-flight operation. */
  const sync = useCallback(() => {
    if (pending.current) return pending.current;
    setSyncing(true);
    const task = Promise.resolve().then(flush).then(() => noteApi.sync())
      .catch((err) => { toast.error(errorMessage(err)); })
      .finally(() => { pending.current = null; setSyncing(false); });
    pending.current = task;
    return task;
  }, [flush]);

  return { syncing, sync };
}
