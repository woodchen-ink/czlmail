/**
 * Wails 绑定的薄封装。
 *
 * 存在的理由有两个: 一是把生成的绑定收口到一处, 上游签名变化时只改这里;
 * 二是在浏览器里直接开发时 window.go 不存在, 需要给出明确的失败而不是
 * 一个看不懂的 undefined 报错。
 */

import { t } from "@/lib/i18n";

import * as App from "@/wailsjs/go/main/App";
import { EventsOn } from "@/wailsjs/runtime/runtime";
import type { main, store, syncer } from "@/wailsjs/go/models";
import { localUrl } from "@/lib/native-bridge";

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
export type HeaderInfo = syncer.HeaderInfo;
export type AuthResult = syncer.AuthResult;
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
export type MailSyncStatus = main.MailSyncStatus;
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
  return localUrl(`/czl-blob?${q.toString()}`);
}

/** 后端事件名，与 Go 侧的常量一一对应。 */
export const Events = {
  connected: "session:connected",
  signInRequired: "session:sign-in-required",
  mailChanged: "mail:changed",
  syncStatus: "mail:sync-status",
  newMail: "mail:new",
  pullProgress: "mail:pull-progress",
  unread: "mail:unread",
  pimChanged: "pim:changed",
  settingsChanged: "settings:changed",
  accountsChanged: "session:accounts-changed",
  openEvent: "calendar:open",
  updateAvailable: "update:available",
  openUpdate: "update:open",
  updateProgress: "update:progress",
  templatesChanged: "templates:changed",
  aiSettingsChanged: "ai-settings:changed",
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
    super(t("Wails 运行时不可用"));
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
  retrySession: guard(App.RetrySession),

  listAccounts: guard(App.ListAccounts),
  listMailboxes: guard(App.ListMailboxes),
  listEmails: guard(App.ListEmails),
  listUnified: guard(App.ListUnified),
  getEmail: guard(App.GetEmail),
  fetchBody: guard(App.FetchBody),
  searchEmails: guard(App.SearchEmails),
  searchEmailsOnServer: guard(App.SearchEmailsOnServer),
  searchAllEmails: guard(App.SearchAllEmails),
  searchAllEmailsOnServer: guard(App.SearchAllEmailsOnServer),
  locateEmail: guard(App.LocateEmail),
  syncNow: guard(App.SyncNow),
  getMailSyncStatus: guard(App.GetMailSyncStatus),
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
  setUILocale: guard(App.SetUILocale),
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
  getHeaderInfo: guard(App.GetHeaderInfo),
  getImageCacheSize: guard(App.GetImageCacheSize),
  clearImageCache: guard(App.ClearImageCache),
  unsubscribe: guard(App.Unsubscribe),
  getInvite: guard(App.GetInvite),
  getPendingUpdate: guard(App.GetPendingUpdate),
  getUnreadTotal: guard(App.GetUnreadTotal),
  getTranslation: guard(App.GetTranslation),
  saveTranslation: guard(App.SaveTranslation),
  addInviteToCalendar: guard(App.AddInviteToCalendar),
  threadEmails: guard(App.ThreadEmails),
  latestInThread: guard(App.LatestInThread),
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
/**
 * 后端报错是「错误码 + 英文」, 这里按错误码换成界面语言的说明并带上码; 没登记的码原样显示。
 * 码与后端的对应: 21xx 更新 (api_update.go), 217x 系统集成 (internal/platform), 22xx 笔记 (internal/notes), 23xx AI (api_ai.go)。
 * 笔记与 AI 曾共用 2202, 没填 API Key 时显示成了笔记冲突, 所以区段要分开。
 */
const ERROR_MESSAGES: Record<string, () => string> = {
  "2002": () => t("还没有登录，请先登录。"),
  "2015": () => t("读取系统钥匙串中的登录凭据失败，请重新登录。"),
  "2018": () => t("无法把登录凭据保存到系统钥匙串，请确认系统钥匙串可用。"),
  "2020": () => t("本机没有找到保存的登录凭据。"),
  "2030": () => t("这个账号没有「已删除」文件夹。"),
  "2031": () => t("这个账号没有「归档」文件夹。"),
  "2033": () => t("标签名不能为空。"),
  "2034": () => t("标签名不能以 $ 开头。"),
  "2035": () => t("标签只能使用英文字母、数字和常见符号，服务器不接受中文等其他字符。"),
  "2038": () => t("请先选择目标文件夹。"),
  "2041": () => t("压缩包里的邮件太多，一次最多导入 5000 封。"),
  "2050": () => t("请填写邮件服务器地址。"),
  "2051": () => t("请填写服务器地址、邮箱和应用专用密码。"),
  "2052": () => t("单点登录需要同时填写身份提供商地址和客户端 ID。"),
  "2060": () => t("认证失败：请检查邮箱地址与应用专用密码（单点登录的密码在这里不能用）。"),
  "2061": () => t("这个地址上没有找到 JMAP 服务，请确认服务器地址。"),
  "2062": () => t("连不上邮件服务器，请检查网络与服务器地址。"),
  "2063": () => t("邮件服务器没有正常响应，请稍后重试。"),
  "2064": () => t("邮件服务器不接受这次浏览器授权得到的令牌（服务器可能把认证交给了外部身份提供商）。请改用应用专用密码登录。"),
  "2065": () => t("登录已过期，保存的令牌无法再续期。"),
  "2080": () => t("这个文件夹正在从服务器拉取，请等它完成。"),
  "2082": () => t("这封邮件没有附件。"),
  "2090": () => t("定时发送的时间无效。"),
  "2101": () => t("请填写标题。"),
  "2102": () => t("开始或结束时间无效。"),
  "2103": () => t("结束时间不能早于开始时间。"),
  "2104": () => t("请选择日历。"),
  "2105": () => t("参加者的邮箱地址无效。"),
  "2108": () => t("你不是这个日程的参加者。"),
  "2109": () => t("请填写日历名称。"),
  "2110": () => t("请填写分组名称。"),
  "2111": () => t("请选择通讯录。"),
  "2112": () => t("邮箱地址无效。"),
  "2114": () => t("请填写通讯录名称。"),
  "2120": () => t("不能把文件夹移动到它自己里面"),
  "2121": () => t("文件夹不能直接下载。"),
  "2122": () => t("名称无效。"),
  "2124": () => t("文件夹层级太深。"),
  "2130": () => t("文件太大，无法导入。"),
  "2132": () => t("文件无法解析，请确认是有效的 .ics 或 .vcf 文件。"),
  "2150": () => t("检查更新失败，请检查网络后重试。"),
  "2151": () => t("更新已经在进行中。"),
  "2152": () => t("没有可用的新版本。"),
  "2154": () => t("安装更新失败，请稍后重试。"),
  "2155": () => t("应用所在文件夹没有写入权限，已在访达中显示新版本，请手动拖入「应用程序」。"),
  "2162": () => t("这不是模板导出文件。"),
  "2170": () => t("当前系统不支持这项功能。"),
  "2171": () => t("CZL Mail 不是从应用包运行的，无法开启这项设置。"),
  "2172": () => t("请先把 CZL Mail 拖到「应用程序」文件夹，再开启开机自启。"),
  "2191": () => t("文件夹里还有邮件，勾选「同时删除其中的邮件」后再删除"),
  "2192": () => t("请先删除或移走子文件夹"),
  "2193": () => t("文件夹名称不能为空，也不能包含 /"),
  "2194": () => t("不能把文件夹移动到它自己里面"),
  "2195": () => t("系统文件夹不能删除或移动"),
  "2200": () => t("笔记已关闭，请在设置中重新开启。"),
  "2201": () => t("这篇笔记刚被其他设备或 AI 更新，你的输入还没有保存，可以另存为恢复笔记。"),
  "2202": () => t("笔记存在版本冲突，请先对比双方内容。"),
  "2203": () => t("文件名称、路径或大小不符合要求，请检查后重试。"),
  "2204": () => t("笔记或附件不存在，请刷新目录后重试。"),
  "2205": () => t("离线或暂时连不上服务器，联网后自动同步，本地内容仍保留。"),
  "2206": () => t("本地保存失败，请检查磁盘空间与目录权限，勿关闭当前编辑。"),
  "2207": () => t("没有写入权限，请检查账号的网盘权限或 AI 助手的笔记写入开关。"),
  "2208": () => t("上一次提交的结果还没有确认，稍后会自动核对。"),
  "2209": () => t("服务器不接受这次写入。内容仍保存在本机。"),
  "2210": () => t("网盘空间不足，无法上传。内容仍保存在本机。"),
  "2211": () => t("文件超过服务器的大小上限，无法同步。"),
  "2220": () => t("这封邮件没有退订链接。"),
  "2222": () => t("退订地址无效。"),
  "2223": () => t("没有可用的发件身份"),
  "2224": () => t("这封邮件没有日历邀请。"),
  "2225": () => t("这个账号没有日历。"),
  "2226": () => t("这封邮件没有请求已读回执。"),
  "2300": () => t("接口地址必须以 http:// 或 https:// 开头。"),
  "2302": () => t("AI 还没有配置好：请填写接口地址、模型名称和 API Key 后保存。"),
  "2309": () => t("AI 没有返回内容（模型可能把输出都用在了思考上），可以把思考档位调低后重试。"),
  "3001": () => t("这个服务器没有提供浏览器授权登录，请改用应用专用密码。"),
  "3006": () => t("服务器地址必须使用 https。"),
  "3021": () => t("浏览器授权超时或已取消。"),
  "3022": () => t("换取令牌失败，请重试；多次失败请改用应用专用密码登录。"),
  "3023": () => t("授权被拒绝。"),
  "3024": () => t("授权状态校验失败，请重新登录。"),
  "3025": () => t("服务器未返回授权码。"),
};

export function errorMessage(e: unknown): string {
  const message = e instanceof Error ? e.message : String(e);
  for (const code of message.match(/\b\d{4}\b/g) ?? []) {
    const localized = ERROR_MESSAGES[code];
    if (localized) return `${localized()} (${code})`;
  }
  return message;
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
