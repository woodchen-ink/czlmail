package store

// migration007 补齐日历、通讯录、文件同步用到的列。
//
// migration001 预建了这些表, 但当时还没对照 Stalwart 的实际返回:
// 日历与通讯录有"默认"标记(新建事件/联系人时落到哪里), 文件节点的 type 列
// 用来区分文件与目录, MIME 类型需要另一列。
const migration007 = `
ALTER TABLE calendars     ADD COLUMN is_default   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE address_books ADD COLUMN is_default   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE file_nodes    ADD COLUMN content_type TEXT    NOT NULL DEFAULT '';
ALTER TABLE file_nodes    ADD COLUMN role         TEXT    NOT NULL DEFAULT '';

-- 联系人检索(收件人补全、按姓名搜索)用的小写拼接文本。
ALTER TABLE contacts ADD COLUMN search_text TEXT NOT NULL DEFAULT '';
`

// migration008 存日历的默认提醒。事件带 useDefaultAlerts 时提醒取自所属日历, 不存就无法到点通知。
// 日历容器每次同步整表替换, 不需要回填。
const migration008 = `
ALTER TABLE calendars ADD COLUMN default_alerts_with_time    TEXT NOT NULL DEFAULT '';
ALTER TABLE calendars ADD COLUMN default_alerts_without_time TEXT NOT NULL DEFAULT '';
`

// migration009 给联系人列表加类别与是否有照片, 并清掉联系人的 state 让下次同步重新引导以回填。
const migration009 = `
ALTER TABLE contacts ADD COLUMN keywords_json TEXT    NOT NULL DEFAULT '[]';
ALTER TABLE contacts ADD COLUMN has_photo     INTEGER NOT NULL DEFAULT 0;
DELETE FROM sync_state WHERE data_type = 'ContactCard';
`

// migration010 存邮件的退订信息(List-Unsubscribe)。NULL 表示还没检查过, 空串表示没有。
// 已缓存正文的旧邮件在打开时按需补查, 不重新拉取正文。
const migration010 = `
ALTER TABLE emails ADD COLUMN list_unsubscribe TEXT;
`

// migration011 邮件头信息里新增已读回执地址, 清空已检查标记让打开时重新补查。
const migration011 = `
UPDATE emails SET list_unsubscribe = NULL;
`

// migration012 缓存 AI 译文: 同一封邮件译成同一种语言只需请求一次模型。
const migration012 = `
CREATE TABLE translations (
    account_id TEXT    NOT NULL,
    email_id   TEXT    NOT NULL,
    language   TEXT    NOT NULL,
    content    TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (account_id, email_id, language)
);
`
