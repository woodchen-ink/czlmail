"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ArrowDownLeft, ArrowUpRight, Loader2, Paperclip } from "lucide-react";
import { toast } from "sonner";

import { useI18n } from "@/components/locale-provider";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { api, errorMessage, type EmailSummary } from "@/lib/api";
import { requestOpenEmail, send } from "@/lib/app-bus";
import { listDate } from "@/lib/format";
import { dateLocale, t } from "@/lib/i18n";
import { cn } from "@/lib/utils";

/** 与后端 store.Direction* 对应: 全部 / 对方发来的 / 发给对方的。 */
type Direction = "" | "from" | "to";

const PAGE = 50;

/**
 * 与联系人的全部往来邮件: 按方向筛选, 按月分组, 触底加载下一页。
 * 后端每翻一页都让各账号服务端补齐这一页, 所以必须从第一页依次往下翻 (见 App.EmailsWith)。
 */
export function Correspondence({ addresses }: { addresses: string[] }) {
  useI18n();
  const [direction, setDirection] = useState<Direction>("");
  const [emails, setEmails] = useState<EmailSummary[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [hasMore, setHasMore] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  const sentinelRef = useRef<HTMLDivElement>(null);
  // 换筛选或换联系人后, 迟到的旧请求不能写进新列表。
  const generation = useRef(0);
  const addrKey = addresses.join("\n");

  /** 取 offset 起的一页并写进列表; 第一页替换, 之后追加。结果只在发起后没换过筛选时才写入。 */
  const load = useCallback(
    (dir: Direction, offset: number) => {
      const gen = generation.current;
      const stale = () => gen !== generation.current;
      return api.emailsWith(addrKey.split("\n"), dir, PAGE, offset).then(
        (page) => {
          if (stale()) return;
          const list = page?.emails ?? [];
          setEmails((prev) => (offset === 0 ? list : [...prev, ...list]));
          setTotal(page?.total ?? 0);
          setHasMore(list.length === PAGE);
          setLoading(false);
        },
        (err) => {
          if (stale()) return;
          toast.error(errorMessage(err));
          setLoading(false);
        },
      );
    },
    [addrKey],
  );

  // 换联系人时父组件按地址重新挂载, 这里只取一次第一页。
  useEffect(() => {
    load("", 0);
  }, [load]);

  function changeDirection(dir: Direction) {
    generation.current += 1;
    setDirection(dir);
    setEmails([]);
    setHasMore(false);
    setLoading(true);
    scrollRef.current?.scrollTo({ top: 0 });
    load(dir, 0);
  }

  function loadMore() {
    setLoading(true);
    load(direction, emails.length);
  }

  // 触底加载: root 是自己的滚动容器, 距底部约一屏半就取下一页。
  useEffect(() => {
    const el = sentinelRef.current;
    if (!el || !hasMore || loading) return;
    const io = new IntersectionObserver(
      (entries) => {
        if (entries[0]?.isIntersecting) loadMore();
      },
      { root: scrollRef.current, rootMargin: "0px 0px 1200px 0px" },
    );
    io.observe(el);
    return () => io.disconnect();
  });

  const mine = useMemo(() => new Set(addrKey.toLowerCase().split("\n")), [addrKey]);
  const months = groupByMonth(emails, dateLocale());

  return (
    <div className="@container flex h-full min-h-0 flex-col">
      <div className="border-border flex shrink-0 items-center gap-3 border-b px-4 py-2 @xl:px-6">
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          value={direction || "all"}
          onValueChange={(v) => v && changeDirection(v === "all" ? "" : (v as Direction))}
        >
          <ToggleGroupItem value="all">{t("全部")}</ToggleGroupItem>
          <ToggleGroupItem value="from">
            <ArrowDownLeft className="size-3.5" />
            {t("对方发来")}
          </ToggleGroupItem>
          <ToggleGroupItem value="to">
            <ArrowUpRight className="size-3.5" />
            {t("我发出的")}
          </ToggleGroupItem>
        </ToggleGroup>
        <span className="text-muted-foreground ml-auto flex items-center gap-1.5 text-xs tabular-nums">
          {loading && <Loader2 className="size-3.5 animate-spin" />}
          {!(loading && emails.length === 0) && t("共 {0} 封", total)}
        </span>
      </div>

      <div ref={scrollRef} className="czl-scroll min-h-0 flex-1 overflow-y-auto">
        {!loading && emails.length === 0 ? (
          <p className="text-muted-foreground py-10 text-center text-sm">{t("没有往来邮件")}</p>
        ) : (
          months.map(([label, items]) => (
            <section key={label}>
              <h3 className="bg-background/95 text-muted-foreground sticky top-0 z-10 px-4 py-1 @xl:px-6 text-xs font-medium backdrop-blur">
                {label}
              </h3>
              {items.map((e) => {
                const incoming = (e.from ?? []).some((a) => mine.has((a.email ?? "").toLowerCase()));
                return (
                  <button
                    key={`${e.accountId}/${e.id}`}
                    type="button"
                    onClick={() => {
                      send("navigate", { module: "mail" });
                      requestOpenEmail({ accountId: e.accountId, emailId: e.id });
                    }}
                    className="hover:bg-secondary focus-visible:ring-ring flex w-full items-start gap-3 px-4 py-2 text-left @xl:px-6 focus-visible:ring-2 focus-visible:outline-none"
                  >
                    <span
                      className={cn("mt-0.5 shrink-0", incoming ? "text-accent" : "text-muted-foreground")}
                      title={incoming ? t("对方发来") : t("我发出的")}
                    >
                      {incoming ? <ArrowDownLeft className="size-4" /> : <ArrowUpRight className="size-4" />}
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="flex items-baseline gap-2">
                        <span className={cn("min-w-0 flex-1 truncate text-sm", e.isUnread && "font-semibold")}>
                          {e.subject || t("(无主题)")}
                        </span>
                        {e.hasAttachment && <Paperclip className="text-muted-foreground size-3.5 shrink-0 self-center" />}
                        <span className="text-muted-foreground shrink-0 text-xs tabular-nums">{listDate(e.receivedAt)}</span>
                      </span>
                      <span className="text-muted-foreground block truncate text-xs">{e.preview}</span>
                    </span>
                  </button>
                );
              })}
            </section>
          ))
        )}
        {loading && emails.length === 0 && (
          <div className="text-muted-foreground flex items-center justify-center gap-2 py-10 text-sm">
            <Loader2 className="size-4 animate-spin" />
            {t("载入中")}
          </div>
        )}
        <div ref={sentinelRef} className="h-px" />
      </div>
    </div>
  );
}

/** 按收件月份分组, 保持原有的时间倒序。 */
function groupByMonth(list: EmailSummary[], locale: string): [string, EmailSummary[]][] {
  const fmt = new Intl.DateTimeFormat(locale, { year: "numeric", month: "long" });
  const out: [string, EmailSummary[]][] = [];
  for (const e of list) {
    const label = fmt.format(new Date(e.receivedAt));
    const last = out[out.length - 1];
    if (last && last[0] === label) last[1].push(e);
    else out.push([label, [e]]);
  }
  return out;
}
