"use client"

import { TriangleAlert } from "lucide-react"

import { CopyButton, CopyField } from "@/components/copy-button"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { useMe } from "@/lib/hooks/use-me"
import { useT } from "@/lib/i18n"
import type { CreatedAppPassword } from "@/lib/types"

/** The only time the full app password is shown, with how to sign in to czlmail. */
export function CreatedDialog({
  created,
  onClose,
}: {
  created: CreatedAppPassword | null
  onClose: () => void
}) {
  const t = useT()
  const me = useMe()
  // The gateway URL czlmail needs is this origin (the admin runs on the same host).
  const server = typeof window === "undefined" ? "" : window.location.origin
  return (
    <Dialog open={!!created} onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        className="sm:max-w-lg"
        onInteractOutside={(e) => e.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle>{t.appPasswords.createdTitle}</DialogTitle>
          <DialogDescription className="flex items-start gap-1.5 text-warning">
            <TriangleAlert className="mt-0.5 size-4 shrink-0" />
            {t.appPasswords.createdOnce}
          </DialogDescription>
        </DialogHeader>
        {created && (
          <div className="grid gap-4">
            <div className="flex items-center gap-2 rounded-lg border bg-muted p-3">
              <code className="min-w-0 flex-1 font-mono text-base break-all select-all">
                {created.password}
              </code>
              <CopyButton
                value={created.password}
                label={t.appPasswords.copyPassword}
                variant="default"
                size="sm"
              />
            </div>
            <div className="grid gap-2">
              <p className="text-sm font-medium">{t.appPasswords.howTo}</p>
              <dl className="grid grid-cols-[4.5rem_1fr] items-center gap-x-3 gap-y-2 text-sm">
                <dt className="text-foreground-muted">
                  {t.appPasswords.howToServer}
                </dt>
                <dd>
                  <CopyField value={server} />
                </dd>
                <dt className="text-foreground-muted">
                  {t.appPasswords.howToUsername}
                </dt>
                <dd>
                  <CopyField value={me.data?.email ?? ""} />
                </dd>
                <dt className="text-foreground-muted">
                  {t.appPasswords.howToPassword}
                </dt>
                <dd className="text-foreground-muted">
                  {t.appPasswords.howToPasswordValue}
                </dd>
              </dl>
              <p className="text-xs text-foreground-subtle">
                {t.appPasswords.howToNote}
              </p>
            </div>
          </div>
        )}
        <DialogFooter>
          <Button onClick={onClose}>{t.common.done}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
