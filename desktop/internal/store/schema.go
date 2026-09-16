package store

// schemaVersion 与 SQLite 的 user_version pragma 对齐, 等于 migrations 的长度。
const schemaVersion = 9

// migrations 按索引顺序执行, 索引 i 把 schema 从版本 i 升到 i+1。
// 已发布的迁移不可修改, 只能追加新的一条。
var migrations = []string{
	migration001,
	migration002,
	migration003,
	migration004,
	migration005,
	migration006,
	migration007,
	migration008,
	migration009,
}

// migration001 建立全部四类数据(邮件/日历/通讯录/文件)的基础表。
//
// 两条贯穿全表的设计:
//
//  1. 所有业务表都以 account_id 打头做复合主键。服务器上有 1 个个人账号加 4 个
//     共享账号, 多账号是既定事实而非将来的扩展, 不存在"单账号快路径"。
//
//  2. JSCalendar (RFC 8984) 与 JSContact (RFC 9553) 的对象是深层嵌套且字段开放的,
//     拆平到列里既存不全也跟不上服务端扩展。因此一律保留 raw_json 原文, 另外把
//     排序和过滤真正用得上的字段抽成列。查询走抽出来的列, 渲染走 raw_json。
const migration001 = `
-- 账号。account_capabilities 决定该账号能同步哪几类数据, 原样存 JSON 数组。
CREATE TABLE accounts (
    id                   TEXT    PRIMARY KEY,
    name                 TEXT    NOT NULL,
    is_personal          INTEGER NOT NULL DEFAULT 0,
    is_read_only         INTEGER NOT NULL DEFAULT 0,
    account_capabilities TEXT    NOT NULL DEFAULT '[]',
    updated_at           INTEGER NOT NULL
);

-- 每 (账号, 数据类型) 一个 state token, 增量同步的锚点。
-- data_type 取 JMAP 的类型名: Mailbox / Email / Thread / Calendar /
-- CalendarEvent / AddressBook / ContactCard / FileNode。
CREATE TABLE sync_state (
    account_id TEXT    NOT NULL,
    data_type  TEXT    NOT NULL,
    state      TEXT    NOT NULL,
    synced_at  INTEGER NOT NULL,
    PRIMARY KEY (account_id, data_type)
) WITHOUT ROWID;

-- 邮箱(文件夹)。role 是 JMAP 的语义角色(inbox/sent/trash/...), 界面上的图标与
-- 排序靠它而不是靠 name, 因为 name 是用户可改的。
CREATE TABLE mailboxes (
    account_id     TEXT    NOT NULL,
    id             TEXT    NOT NULL,
    parent_id      TEXT,
    name           TEXT    NOT NULL,
    role           TEXT,
    sort_order     INTEGER NOT NULL DEFAULT 0,
    total_emails   INTEGER NOT NULL DEFAULT 0,
    unread_emails  INTEGER NOT NULL DEFAULT 0,
    total_threads  INTEGER NOT NULL DEFAULT 0,
    unread_threads INTEGER NOT NULL DEFAULT 0,
    my_rights      TEXT    NOT NULL DEFAULT '{}',
    is_subscribed  INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (account_id, id)
);

CREATE INDEX idx_mailboxes_parent ON mailboxes (account_id, parent_id);
CREATE INDEX idx_mailboxes_role   ON mailboxes (account_id, role);

-- 邮件元数据。正文单独按需拉取: body_fetched_at 为 NULL 表示只有元数据。
-- 列表视图只依赖本表, 不触发正文请求。
CREATE TABLE emails (
    account_id      TEXT    NOT NULL,
    id              TEXT    NOT NULL,
    blob_id         TEXT    NOT NULL DEFAULT '',
    thread_id       TEXT    NOT NULL DEFAULT '',
    subject         TEXT    NOT NULL DEFAULT '',
    from_json       TEXT    NOT NULL DEFAULT '[]',
    to_json         TEXT    NOT NULL DEFAULT '[]',
    cc_json         TEXT    NOT NULL DEFAULT '[]',
    bcc_json        TEXT    NOT NULL DEFAULT '[]',
    reply_to_json   TEXT    NOT NULL DEFAULT '[]',
    sent_at         INTEGER,
    received_at     INTEGER NOT NULL DEFAULT 0,
    size            INTEGER NOT NULL DEFAULT 0,
    preview         TEXT    NOT NULL DEFAULT '',
    has_attachment  INTEGER NOT NULL DEFAULT 0,
    message_id      TEXT    NOT NULL DEFAULT '',
    in_reply_to     TEXT    NOT NULL DEFAULT '',
    body_text       TEXT,
    body_html       TEXT,
    body_structure  TEXT,
    body_fetched_at INTEGER,
    PRIMARY KEY (account_id, id)
);

-- 列表默认按收件时间倒序, 这条索引是邮件列表唯一的排序依据。
CREATE INDEX idx_emails_received ON emails (account_id, received_at DESC);
CREATE INDEX idx_emails_thread   ON emails (account_id, thread_id);

-- 一封邮件可同时属于多个邮箱, JMAP 的 mailboxIds 是集合而非单值。
CREATE TABLE email_mailboxes (
    account_id TEXT NOT NULL,
    email_id   TEXT NOT NULL,
    mailbox_id TEXT NOT NULL,
    PRIMARY KEY (account_id, email_id, mailbox_id)
) WITHOUT ROWID;

-- 按邮箱翻页的覆盖索引, 避免回表。
CREATE INDEX idx_email_mailboxes_box ON email_mailboxes (account_id, mailbox_id, email_id);

-- 关键字既是系统标志($seen/$flagged/$draft/...)也是用户自定义标签, JMAP 不区分两者。
CREATE TABLE email_keywords (
    account_id TEXT NOT NULL,
    email_id   TEXT NOT NULL,
    keyword    TEXT NOT NULL,
    PRIMARY KEY (account_id, email_id, keyword)
) WITHOUT ROWID;

CREATE INDEX idx_email_keywords_kw ON email_keywords (account_id, keyword, email_id);

-- 线程。email_ids 保留服务端给出的顺序, 故存 JSON 数组而非另开关联表。
CREATE TABLE threads (
    account_id TEXT NOT NULL,
    id         TEXT NOT NULL,
    email_ids  TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (account_id, id)
);

-- 日历容器。
CREATE TABLE calendars (
    account_id    TEXT    NOT NULL,
    id            TEXT    NOT NULL,
    name          TEXT    NOT NULL DEFAULT '',
    description   TEXT    NOT NULL DEFAULT '',
    color         TEXT    NOT NULL DEFAULT '',
    sort_order    INTEGER NOT NULL DEFAULT 0,
    is_visible    INTEGER NOT NULL DEFAULT 1,
    is_subscribed INTEGER NOT NULL DEFAULT 1,
    my_rights     TEXT    NOT NULL DEFAULT '{}',
    PRIMARY KEY (account_id, id)
);

-- 日历事件。start_at / end_at 存 UTC 秒, 仅供范围查询;
-- 展示用的本地时间与重复规则一律从 raw_json 还原, 不依赖这两列。
-- 重复事件在此存主体一行, 展开成具体occurrence由上层按视图区间计算, 不落库。
CREATE TABLE calendar_events (
    account_id   TEXT    NOT NULL,
    id           TEXT    NOT NULL,
    calendar_ids TEXT    NOT NULL DEFAULT '[]',
    uid          TEXT    NOT NULL DEFAULT '',
    title        TEXT    NOT NULL DEFAULT '',
    description  TEXT    NOT NULL DEFAULT '',
    location     TEXT    NOT NULL DEFAULT '',
    start_at     INTEGER,
    end_at       INTEGER,
    is_all_day   INTEGER NOT NULL DEFAULT 0,
    time_zone    TEXT    NOT NULL DEFAULT '',
    is_recurring INTEGER NOT NULL DEFAULT 0,
    status       TEXT    NOT NULL DEFAULT '',
    raw_json     TEXT    NOT NULL DEFAULT '{}',
    updated_at   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (account_id, id)
);

-- 月/周/日视图都是按时间区间取事件, 这条索引服务全部三种视图。
CREATE INDEX idx_calendar_events_range ON calendar_events (account_id, start_at, end_at);
CREATE INDEX idx_calendar_events_uid   ON calendar_events (account_id, uid);

-- 通讯录容器。
CREATE TABLE address_books (
    account_id    TEXT    NOT NULL,
    id            TEXT    NOT NULL,
    name          TEXT    NOT NULL DEFAULT '',
    description   TEXT    NOT NULL DEFAULT '',
    sort_order    INTEGER NOT NULL DEFAULT 0,
    is_subscribed INTEGER NOT NULL DEFAULT 1,
    my_rights     TEXT    NOT NULL DEFAULT '{}',
    PRIMARY KEY (account_id, id)
);

-- 联系人。emails_json 供收件人自动补全检索, 其余结构化字段从 raw_json 还原。
CREATE TABLE contacts (
    account_id       TEXT    NOT NULL,
    id               TEXT    NOT NULL,
    address_book_ids TEXT    NOT NULL DEFAULT '[]',
    uid              TEXT    NOT NULL DEFAULT '',
    kind             TEXT    NOT NULL DEFAULT '',
    display_name     TEXT    NOT NULL DEFAULT '',
    sort_name        TEXT    NOT NULL DEFAULT '',
    emails_json      TEXT    NOT NULL DEFAULT '[]',
    phones_json      TEXT    NOT NULL DEFAULT '[]',
    organization     TEXT    NOT NULL DEFAULT '',
    photo_blob_id    TEXT    NOT NULL DEFAULT '',
    raw_json         TEXT    NOT NULL DEFAULT '{}',
    updated_at       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (account_id, id)
);

CREATE INDEX idx_contacts_sort ON contacts (account_id, sort_name);

-- 文件节点。Stalwart 的能力名是 urn:ietf:params:jmap:filenode, 不是标准的 files。
-- type 区分目录与文件, 目录没有 blob_id。
CREATE TABLE file_nodes (
    account_id  TEXT    NOT NULL,
    id          TEXT    NOT NULL,
    parent_id   TEXT,
    name        TEXT    NOT NULL DEFAULT '',
    type        TEXT    NOT NULL DEFAULT '',
    blob_id     TEXT    NOT NULL DEFAULT '',
    size        INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL DEFAULT 0,
    modified_at INTEGER NOT NULL DEFAULT 0,
    my_rights   TEXT    NOT NULL DEFAULT '{}',
    raw_json    TEXT    NOT NULL DEFAULT '{}',
    PRIMARY KEY (account_id, id)
);

CREATE INDEX idx_file_nodes_parent ON file_nodes (account_id, parent_id, name);
`

// migration002 建立邮件全文索引。
//
// 分词器选 trigram 而不是默认的 unicode61: unicode61 按空白与标点切词, 遇到中文会
// 把整段连续的汉字当成一个 token, 搜"发票"匹配不到"增值税发票申请"。trigram 按三字
// 滑窗建索引, 对中文与英文都能做子串匹配。代价是索引体积明显变大, 且查询词至少要
// 三个字符 —— 对邮件搜索是可以接受的取舍。
//
// 用 external content 表(content='emails'), FTS 只存索引不存正文副本,
// 否则每封邮件的正文会在库里存两份。
const migration002 = `
CREATE VIRTUAL TABLE emails_fts USING fts5(
    subject,
    preview,
    body_text,
    content='emails',
    content_rowid='rowid',
    tokenize='trigram'
);

-- external content 表必须靠触发器同步, FTS5 不会自动跟随源表变化。
CREATE TRIGGER emails_fts_insert AFTER INSERT ON emails BEGIN
    INSERT INTO emails_fts(rowid, subject, preview, body_text)
    VALUES (new.rowid, new.subject, new.preview, COALESCE(new.body_text, ''));
END;

CREATE TRIGGER emails_fts_delete AFTER DELETE ON emails BEGIN
    INSERT INTO emails_fts(emails_fts, rowid, subject, preview, body_text)
    VALUES ('delete', old.rowid, old.subject, old.preview, COALESCE(old.body_text, ''));
END;

-- 更新必须先删旧行再插新行: FTS5 的 external content 模式下直接 UPDATE
-- 会让索引与源表失配, 且失配不会报错, 只会让搜索悄悄漏结果。
CREATE TRIGGER emails_fts_update AFTER UPDATE ON emails BEGIN
    INSERT INTO emails_fts(emails_fts, rowid, subject, preview, body_text)
    VALUES ('delete', old.rowid, old.subject, old.preview, COALESCE(old.body_text, ''));
    INSERT INTO emails_fts(rowid, subject, preview, body_text)
    VALUES (new.rowid, new.subject, new.preview, COALESCE(new.body_text, ''));
END;

-- 已有数据补建索引。首次升级时 emails 表里可能已有上万行。
INSERT INTO emails_fts(rowid, subject, preview, body_text)
SELECT rowid, subject, preview, COALESCE(body_text, '') FROM emails;
`

// migration003 建立远程内容的信任名单与应用设置。
//
// 远程图片默认全部拦截: 一张一像素的图片被加载, 就等于向发件人确认这封信
// 在什么时间、被哪个 IP 打开过。营销与钓鱼邮件都依赖这个信号。
//
// 信任粒度是完整地址而不是域名。按域名信任意味着"信任 gmail.com 的所有人",
// 而垃圾邮件与钓鱼邮件恰恰大量来自大型邮件服务商的域。
const migration003 = `
CREATE TABLE trusted_senders (
    account_id TEXT    NOT NULL,
    address    TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (account_id, address)
) WITHOUT ROWID;

-- 应用级设置的键值表。设置项少且形态各异, 一项一列会让每加一个开关
-- 都要走一次 schema 迁移。
CREATE TABLE app_settings (
    key        TEXT    PRIMARY KEY,
    value      TEXT    NOT NULL,
    updated_at INTEGER NOT NULL
) WITHOUT ROWID;
`

// migration004 建立邮件模板。
//
// 模板本地存 SQLite, 真相在服务器的文件存储里(JMAP filenode / WebDAV),
// 以一个 JSON 文件的形式跨设备同步。本地表是缓存与离线副本。
//
// 为同步预留的两列, 现在就建好, 免得将来再加一次迁移:
//
//   - deleted_at 做墓碑。删除必须留痕, 否则同步时无法区分"这台设备删了它"与
//     "那台设备刚新建了它", 被删的模板会从别的设备上原样同步回来。
//   - remote_rev 记录上次同步时远端的版本, 用于检测并发修改。
//
// 模板不绑定账号: 同一段话术在个人邮箱与共享邮箱里都可能用到,
// 按账号隔离只会逼用户把同一个模板录入多次。
const migration004 = `
CREATE TABLE email_templates (
    id            TEXT    PRIMARY KEY,
    name          TEXT    NOT NULL,
    category      TEXT    NOT NULL DEFAULT '',
    subject       TEXT    NOT NULL DEFAULT '',
    body          TEXT    NOT NULL DEFAULT '',
    is_html       INTEGER NOT NULL DEFAULT 0,
    default_to    TEXT    NOT NULL DEFAULT '[]',
    default_cc    TEXT    NOT NULL DEFAULT '[]',
    default_bcc   TEXT    NOT NULL DEFAULT '[]',
    identity_id   TEXT    NOT NULL DEFAULT '',
    is_favorite   INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    deleted_at    INTEGER,
    remote_rev    TEXT    NOT NULL DEFAULT ''
);

-- 收藏的排前面, 其次按分类与名称。墓碑不参与列表, 故带上过滤条件。
CREATE INDEX idx_templates_order
    ON email_templates (is_favorite DESC, category, name)
    WHERE deleted_at IS NULL;
`

// migration005 建立发件人头像缓存。
//
// key 形如 "g:<邮箱哈希>"(Gravatar) 或 "f:<可注册域>"(域名图标)。
// missing=1 是负缓存: 绝大多数企业发件人没有 Gravatar, 不记下"查过没有",
// 每次滚动列表都会对同一批地址重复发请求。
const migration005 = `
CREATE TABLE avatar_cache (
    key          TEXT    PRIMARY KEY,
    content_type TEXT    NOT NULL DEFAULT '',
    data         BLOB,
    missing      INTEGER NOT NULL DEFAULT 0,
    fetched_at   INTEGER NOT NULL
) WITHOUT ROWID;
`

// migration006 为邮件增加附件清单。
//
// 与正文一样按需填充, 在用户打开邮件拉正文时一并写入; 列表视图只靠
// has_attachment 显示回形针, 不需要清单本身。
const migration006 = `
ALTER TABLE emails ADD COLUMN attachments_json TEXT NOT NULL DEFAULT '[]';

-- 升级前已缓存正文的带附件邮件, 清单是空的且不会再触发拉取。
-- 让它们的正文缓存失效, 下次打开时连同附件清单一起重拉。
UPDATE emails SET body_fetched_at = NULL WHERE has_attachment = 1;
`
