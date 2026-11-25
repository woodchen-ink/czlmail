import test from "node:test";
import assert from "node:assert/strict";
import { allowDraftLeave, beginDraftIntent, hasDraftContent } from "./draft-transition.ts";
import { threadRows } from "./thread-rows.ts";

test("Cc-only and Bcc-only drafts are not empty", () => {
  assert.equal(hasDraftContent("", "a@example.org", "", "", false, 0), true);
  assert.equal(hasDraftContent("", "", "a@example.org", "", false, 0), true);
  assert.equal(hasDraftContent(" ", "", "", "", false, 0), false);
});
test("failed save retains the editor; a successful retry allows leaving", async () => {
  assert.equal(await allowDraftLeave(async () => false, () => 1), false);
  assert.equal(await allowDraftLeave(async () => true, () => 1), true);
});
test("typing while a save is pending cancels the switch", async () => {
  let rev = 1;
  const write = Promise.withResolvers();
  const leave = allowDraftLeave(() => write.promise, () => rev);
  rev++; write.resolve(true);
  assert.equal(await leave, false);
});
test("a newer reply or navigation invalidates pending lookups", async () => {
  const clock = { current: 0 };
  const slow = Promise.withResolvers();
  const a = beginDraftIntent(clock);
  const first = slow.promise.then(() => a());
  const b = beginDraftIntent(clock);
  assert.equal(b(), true);
  slow.resolve(); assert.equal(await first, false);
  beginDraftIntent(clock); assert.equal(b(), false);
});
test("draft navigation keeps both messages in a shared thread", () => {
  const emails = ["a", "b"].map(id => ({ id, accountId: "one", threadId: "same", receivedAt: "2026-01-01", isUnread: false }));
  assert.deepEqual(threadRows(emails, true).map(r => r.head.id), ["a", "b"]);
  assert.equal(threadRows(emails).length, 1);
});
