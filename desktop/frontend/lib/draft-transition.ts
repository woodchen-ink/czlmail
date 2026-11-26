/** Every lookup and pane switch competes for the same user intent. */
export function beginDraftIntent(clock: { current: number }) {
  const ticket = ++clock.current;
  return () => clock.current === ticket;
}

export function hasDraftContent(to: string, cc: string, bcc: string, subject: string, body: boolean, attachments: number) {
  return Boolean(to.trim() || cc.trim() || bcc.trim() || subject.trim() || body || attachments);
}

/** Do not unmount on failure, or when the user edited again during the write. */
export async function allowDraftLeave(write: () => Promise<boolean>, revision: () => number) {
  const at = revision();
  return (await write()) && at === revision();
}
