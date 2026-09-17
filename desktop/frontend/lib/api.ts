/**
 * Wails 绑定的薄封装。
 *
 * 存在的理由有两个: 一是把生成的绑定收口到一处, 上游签名变化时只改这里;
 * 二是在浏览器里直接开发时 window.go 不存在, 需要给出明确的失败而不是
 * 一个看不懂的 undefined 报错。
 */

import * as App from "@/wailsjs/go/main/App";
import { EventsOn } from "@/wailsjs/runtime/runtime";
import type { main, store } from "@/wailsjs/go/models";

export type Account = store.Account;
export type Mailbox = store.Mailbox;
export type EmailSummary = store.EmailSummary;
export type EmailDetail = store.EmailDetail;
export type Address = store.Address;
export type Identity = main.Identity;
export type SessionStatus = main.SessionStatus;
export type AppSettings = main.AppSettings;
export type ComposeRequest = main.ComposeRequest;
export type Attachment = store.Attachment;
export type UploadedAttachment = main.UploadedAttachment;
export type Calendar = store.Calendar;
export type AddressBook = store.AddressBook;
export type ContactSummary = store.ContactSummary;
export type FileNode = store.FileNode;
export type Recipient = store.Recipient;
export type Template = store.Template;
export type EmailWithAccount = store.EmailWithAccount;
export type EventOccurrence = main.EventOccurrence;
export type EventDetail = main.EventDetail;
export type EventInput = main.EventInput;
export type ContactForm = main.ContactForm;
export type ContactField = main.ContactField;
export type Recurrence = main.Recurrence;
export type MCPStatus = main.MCPStatus;
export type TaskItem = main.TaskItem;
export type AIConfig = main.AIConfig;
export type FilterState = main.FilterState;
export type Vacation = main.Vacation;
export type IntegrationStatus = main.IntegrationStatus;
export type OpenRequest = main.OpenRequest;
export type ScheduledItem = main.ScheduledItem;
export type UpdateInfo = main.UpdateInfo;
export type Invite = main.Invite;
export type SignInHint = main.SignInHint;
export type Unsubscribe = store.Unsubscribe;
export type EmailLocation = store.EmailLocation;
/** 整夹拉取进度，随 Events.pullProgress 推送。只经事件传递，Wails 不会为它生成模型。 */
export interface PullStatus {
  accountId: string;
  mailboxId: string;
  scanned: number;
  fetched: number;
  total: number;
  done: boolean;
  error: string;
}

/**
 * 可在应用内预览的类型，是 Go 侧 previewableTypes 的子集。这里只决定是否显示预览入口，
 * 真正的安全边界在 Go。PDF 不做应用内预览：WebView2 的 PDF 查看器加载不了本地资源路径，
 * 交给系统的 PDF 程序打开体验也更好。
 */
const PREVIEWABLE = new Set([
  "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "text/plain",
]);

export function isPreviewable(type: string): boolean {
  return PREVIEWABLE.has(type.split(";")[0].trim().toLowerCase());
}

/** 附件的本地预览地址，由 Go 代理下载。 */
export function blobUrl(accountId: string, a: { blobId: string; type: string; name?: string }): string {
  const q = new URLSearchParams({ account: accountId, blob: a.blobId, type: a.type });
  return `/czl-blob?${q.toString()}`;
}

/** 后端事件名，与 Go 侧的常量一一对应。 */
export const Events = {
  connected: "session:connected",
  signInRequired: "session:sign-in-required",
  mailChanged: "mail:changed",
  openEmail: "mail:open",
  newMail: "mail:new",
  pullProgress: "mail:pull-progress",
  pimChanged: "pim:changed",
  openEvent: "calendar:open",
  updateAvailable: "update:available",
  openUpdate: "update:open",
  updateProgress: "update:progress",
  templatesChanged: "templates:changed",
  aiStream: "ai:stream",
  openRequest: "app:open-request",
} as const;

/** Events.pimChanged 的负载。type 为空表示该账号全部类型都可能变了。 */
export interface PIMChange {
  accountId: string;
  type: "" | "Calendar" | "CalendarEvent" | "AddressBook" | "ContactCard" | "FileNode";
}

/** 运行在 Wails 容器内时为 true。浏览器直开调试时为 false。 */
export function hasRuntime(): boolean {
  return typeof window !== "undefined" && "go" in window;
}

class NoRuntimeError extends Error {
  constructor() {
    super("Wails 运行时不可用");
    this.name = "NoRuntimeError";
  }
}

function guard<T extends unknown[], R>(fn: (...args: T) => Promise<R>) {
  return (...args: T): Promise<R> => {
    if (!hasRuntime()) return Promise.reject(new NoRuntimeError());
    return fn(...args);
  };
}

export const api = {
  getSessionStatus: guard(App.GetSessionStatus),
  probeServer: guard(App.ProbeServer),
  signIn: guard(App.SignIn),
  signInWithPassword: guard(App.SignInWithPassword),
  cancelSignIn: guard(App.CancelSignIn),
  signOut: guard(App.SignOut),

  listAccounts: guard(App.ListAccounts),
  listMailboxes: guard(App.ListMailboxes),
  listEmails: guard(App.ListEmails),
  getEmail: guard(App.GetEmail),
  fetchBody: guard(App.FetchBody),
  searchEmails: guard(App.SearchEmails),
  locateEmail: guard(App.LocateEmail),
  syncNow: guard(App.SyncNow),
  pullMailbox: guard(App.PullMailbox),

  markRead: guard(App.MarkRead),
  markFlagged: guard(App.MarkFlagged),
  moveEmails: guard(App.MoveEmails),
  trashEmails: guard(App.TrashEmails),
  deleteEmails: guard(App.DeleteEmails),
  setPinned: guard(App.SetPinned),
  markMailboxRead: guard(App.MarkMailboxRead),
  emptyMailbox: guard(App.EmptyMailbox),

  listIdentities: guard(App.ListIdentities),
  sendEmail: guard(App.SendEmail),

  archiveEmails: guard(App.ArchiveEmails),
  markJunk: guard(App.MarkJunk),
  setLabel: guard(App.SetLabel),
  listLabels: guard(App.ListLabels),
  viewSource: guard(App.ViewSource),
  exportEmail: guard(App.ExportEmail),
  importEmails: guard(App.ImportEmails),

  listTemplates: guard(App.ListTemplates),
  saveTemplate: guard(App.SaveTemplate),
  deleteTemplate: guard(App.DeleteTemplate),
  applyTemplate: guard(App.ApplyTemplate),

  saveAttachment: guard(App.SaveAttachment),
  addAttachments: guard(App.AddAttachments),
  openAttachment: guard(App.OpenAttachment),
  revealAttachment: guard(App.RevealAttachment),
  downloadAllAttachments: guard(App.DownloadAllAttachments),

  isSenderTrusted: guard(App.IsSenderTrusted),
  trustSender: guard(App.TrustSender),
  untrustSender: guard(App.UntrustSender),
  listTrustedSenders: guard(App.ListTrustedSenders),
  getSettings: guard(App.GetSettings),
  saveSettings: guard(App.SaveSettings),
  getVersion: guard(App.GetVersion),

  listCalendars: guard(App.ListCalendars),
  listEvents: guard(App.ListEvents),
  getEvent: guard(App.GetEvent),
  saveEvent: guard(App.SaveEvent),
  deleteEvent: guard(App.DeleteEvent),
  moveEvent: guard(App.MoveEvent),
  setCalendarVisible: guard(App.SetCalendarVisible),
  importCalendarFile: guard(App.ImportCalendarFile),

  listAddressBooks: guard(App.ListAddressBooks),
  listContacts: guard(App.ListContacts),
  getContact: guard(App.GetContact),
  saveContact: guard(App.SaveContact),
  deleteContacts: guard(App.DeleteContacts),
  searchRecipients: guard(App.SearchRecipients),
  searchDirectory: guard(App.SearchDirectory),
  attachPaths: guard(App.AttachPaths),
  discoverSignIn: guard(App.DiscoverSignIn),
  deleteAllData: guard(App.DeleteAllData),
  recentEmailsWith: guard(App.RecentEmailsWith),
  upcomingEventsWith: guard(App.UpcomingEventsWith),
  importContactsFile: guard(App.ImportContactsFile),

  listFiles: guard(App.ListFiles),
  searchFiles: guard(App.SearchFiles),
  filePath: guard(App.FilePath),
  createFolder: guard(App.CreateFolder),
  uploadFiles: guard(App.UploadFiles),
  renameFile: guard(App.RenameFile),
  moveFiles: guard(App.MoveFiles),
  deleteFiles: guard(App.DeleteFiles),
  openFile: guard(App.OpenFile),
  saveFileAs: guard(App.SaveFileAs),

  updateSignature: guard(App.UpdateSignature),
  templatePlaceholders: guard(App.TemplatePlaceholders),
  importTemplatesFile: guard(App.ImportTemplatesFile),
  syncTemplates: guard(App.SyncTemplates),

  getMCPStatus: guard(App.GetMCPStatus),
  setMCP: guard(App.SetMCP),
  resetMCPToken: guard(App.ResetMCPToken),

  checkForUpdate: guard(App.CheckForUpdate),
  installUpdate: guard(App.InstallUpdate),

  cancelAI: guard(App.CancelAI),
  cancelScheduledSend: guard(App.CancelScheduledSend),
  copyFiles: guard(App.CopyFiles),
  createMailbox: guard(App.CreateMailbox),
  createTextFile: guard(App.CreateTextFile),
  deleteAddressBook: guard(App.DeleteAddressBook),
  deleteCalendar: guard(App.DeleteCalendar),
  deleteMailbox: guard(App.DeleteMailbox),
  deleteTask: guard(App.DeleteTask),
  discardDraft: guard(App.DiscardDraft),
  duplicateFile: guard(App.DuplicateFile),
  fileFavorites: guard(App.FileFavorites),
  getAIConfig: guard(App.GetAIConfig),
  getFilters: guard(App.GetFilters),
  getIntegrationStatus: guard(App.GetIntegrationStatus),
  getVacation: guard(App.GetVacation),
  importCalendarSource: guard(App.ImportCalendarSource),
  listScheduledSends: guard(App.ListScheduledSends),
  listTasks: guard(App.ListTasks),
  maxDelayedSend: guard(App.MaxDelayedSend),
  moveContacts: guard(App.MoveContacts),
  moveMailbox: guard(App.MoveMailbox),
  openDefaultAppsSettings: guard(App.OpenDefaultAppsSettings),
  prefetchBodies: guard(App.PrefetchBodies),
  renameMailbox: guard(App.RenameMailbox),
  respondEvent: guard(App.RespondEvent),
  saveAIConfig: guard(App.SaveAIConfig),
  saveAddressBook: guard(App.SaveAddressBook),
  saveCalendar: guard(App.SaveCalendar),
  saveDraft: guard(App.SaveDraft),
  saveFilters: guard(App.SaveFilters),
  saveSieveScript: guard(App.SaveSieveScript),
  saveTask: guard(App.SaveTask),
  saveVacation: guard(App.SaveVacation),
  setAutostart: guard(App.SetAutostart),
  setFileFavorite: guard(App.SetFileFavorite),
  setTaskCompleted: guard(App.SetTaskCompleted),
  startAI: guard(App.StartAI),
  takeOpenRequests: guard(App.TakeOpenRequests),
  testAI: guard(App.TestAI),
  updateSignatureHTML: guard(App.UpdateSignatureHTML),
  uploadFolder: guard(App.UploadFolder),
  uploadPaths: guard(App.UploadPaths),
  loadListHeaders: guard(App.LoadListHeaders),
  unsubscribe: guard(App.Unsubscribe),
  getInvite: guard(App.GetInvite),
  getPendingUpdate: guard(App.GetPendingUpdate),
  getTranslation: guard(App.GetTranslation),
  saveTranslation: guard(App.SaveTranslation),
  addInviteToCalendar: guard(App.AddInviteToCalendar),
  threadEmails: guard(App.ThreadEmails),
  sendReadReceipt: guard(App.SendReadReceipt),
  ignoreReadReceipt: guard(App.IgnoreReadReceipt),
};

/** 订阅后端事件，返回取消订阅函数。 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function onEvent(name: string, handler: (...data: any[]) => void): () => void {
  if (!hasRuntime()) return () => {};
  return EventsOn(name, handler);
}

/** 把后端 error 转成可读文案。Go 侧的错误是「数字码 + 英文短语」。 */
export function errorMessage(e: unknown): string {
  if (e instanceof NoRuntimeError) return e.message;
  if (e instanceof Error) return e.message;
  return String(e);
}

/** Events.aiStream 的负载。 */
export interface AIChunk {
  id: string;
  delta: string;
  done: boolean;
  error: string;
}

/**
 * 运行一个 AI 任务, 流式回调增量文本, 结束时 resolve 完整结果。
 * signal 中止时取消后端任务。
 */
export function runAI(
  req: main.AIRequest,
  onDelta: (full: string) => void,
  signal?: AbortSignal,
): Promise<string> {
  return new Promise((resolve, reject) => {
    let id = "";
    let full = "";
    const pending: AIChunk[] = [];
    const handle = (c: AIChunk) => {
      if (c.delta) {
        full += c.delta;
        onDelta(full);
      }
      if (c.done) {
        off();
        if (c.error) reject(new Error(c.error));
        else resolve(full);
      }
    };
    const off = onEvent(Events.aiStream, (c: AIChunk) => {
      if (!id) {
        pending.push(c);
        return;
      }
      if (c.id === id) handle(c);
    });
    signal?.addEventListener("abort", () => {
      if (id) void App.CancelAI(id);
      off();
      reject(new DOMException("aborted", "AbortError"));
    });
    App.StartAI(req)
      .then((taskId) => {
        id = taskId;
        for (const c of pending.splice(0)) if (c.id === id) handle(c);
      })
      .catch((err) => {
        off();
        reject(err);
      });
  });
}
