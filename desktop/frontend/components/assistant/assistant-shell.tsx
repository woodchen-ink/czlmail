"use client";

import { useCallback, useEffect, useState } from "react";
import {
  Bot,
  Check,
  Copy,
  Eye,
  EyeOff,
  KeyRound,
  Loader2,
  MessageSquareText,
  Send,
  ShieldAlert,
  Terminal,
} from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { cn } from "@/lib/utils";
import { api, errorMessage, type MCPStatus } from "@/lib/api";

const TOOLS: { name: string; desc: string; write?: boolean }[] = [
  { name: "list_accounts", desc: "列出个人邮箱与共享邮箱" },
  { name: "list_mailboxes", desc: "列出文件夹与未读数" },
  { name: "list_emails", desc: "按时间列出某个文件夹的邮件" },
  { name: "search_emails", desc: "全文搜索本地已同步的邮件" },
  { name: "read_email", desc: "读取邮件正文与附件清单" },
  { name: "mark_read", desc: "标记已读 / 未读", write: true },
  { name: "send_email", desc: "发送纯文本邮件（需单独开启）", write: true },
  { name: "list_events", desc: "查询时间段内的日程" },
  { name: "create_event", desc: "在默认日历里新建日程", write: true },
  { name: "search_contacts", desc: "搜索通讯录" },
  { name: "list_files", desc: "浏览网盘文件" },
];

const EXAMPLES = [
  "帮我总结一下今天收到的未读邮件，按重要程度排序",
  "查一下上个月 DHL 发来的清关通知，列出运单号",
  "我下周三下午有没有空？如果有，加一个 15:00 的客户会议",
  "找到张经理的邮箱，帮我起草一封询价邮件（先给我看，不要直接发）",
];

export function AssistantShell({ active }: { active: boolean }) {
  const [status, setStatus] = useState<MCPStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirmSend, setConfirmSend] = useState(false);
  const [confirmReset, setConfirmReset] = useState(false);

  const load = useCallback(async () => {
    try {
      setStatus(await api.getMCPStatus());
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }, []);

  useEffect(() => {
    if (active) load();
  }, [active, load]);

  async function apply(enabled: boolean, allowSend: boolean) {
    setBusy(true);
    try {
      setStatus(await api.setMCP(enabled, allowSend));
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function resetToken() {
    setBusy(true);
    try {
      setStatus(await api.resetMCPToken());
      toast.success("已更换令牌，通过 HTTP 直连的客户端需要更新配置");
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const exe = status?.executable ?? "czlmail.exe";
  const desktopConfig = JSON.stringify({ mcpServers: { czlmail: { command: exe, args: ["mcp"] } } }, null, 2);
  const codeStdio = `claude mcp add --scope user czlmail -- "${exe}" mcp`;
  const codeHttp = status?.url
    ? `claude mcp add --scope user --transport http czlmail ${status.url} --header "Authorization: Bearer ${status.token}"`
    : "";

  return (
    <ScrollArea className="h-full">
      <div className="mx-auto flex max-w-3xl flex-col gap-6 p-8">
        <header className="flex items-start gap-4">
          <div className="bg-secondary flex size-12 shrink-0 items-center justify-center rounded-md">
            <Bot className="size-6" />
          </div>
          <div>
            <h1 className="text-xl font-semibold">AI 助手</h1>
            <p className="text-muted-foreground mt-1 text-sm leading-relaxed">
              通过 MCP（Model Context Protocol），让本机的 Claude Desktop、Claude Code 等 AI 助手读取你的邮件、日程、通讯录和文件，
              帮你总结邮件、查找信息、安排日程。数据只在本机的 CZL Mail 与 AI 客户端之间传递，不经过任何第三方服务器。
            </p>
          </div>
        </header>

        <Card>
          <Row
            title="启用 MCP"
            desc={
              status?.running
                ? `正在运行：${status.url}`
                : "开启后 CZL Mail 会在本机提供 MCP 服务。CZL Mail 需要保持运行（关闭窗口后会留在托盘）。"
            }
          >
            {busy && <Loader2 className="text-muted-foreground size-4 animate-spin" />}
            <Switch
              checked={!!status?.enabled}
              disabled={!status || busy}
              onCheckedChange={(on) => apply(on, on ? !!status?.allowSend : false)}
            />
          </Row>
          <Divider />
          <Row
            title="允许 AI 发送邮件"
            desc="默认关闭。开启后 AI 可以直接以你的名义发信；邮件内容里可能藏有诱导 AI 发信的指令，建议保持关闭，让 AI 只起草、由你来发。"
            icon={<Send className="text-muted-foreground size-4" />}
          >
            <Switch
              checked={!!status?.allowSend}
              disabled={!status?.enabled || busy}
              onCheckedChange={(on) => (on ? setConfirmSend(true) : apply(true, false))}
            />
          </Row>
        </Card>

        <section className={cn("flex flex-col gap-3", !status?.enabled && "opacity-60")}>
          <h2 className="text-sm font-semibold">连接 AI 客户端</h2>
          <Tabs defaultValue="desktop">
            <TabsList>
              <TabsTrigger value="desktop">Claude Desktop</TabsTrigger>
              <TabsTrigger value="code">Claude Code</TabsTrigger>
              <TabsTrigger value="other">其它客户端</TabsTrigger>
            </TabsList>

            <TabsContent value="desktop" className="mt-3">
              <Card>
                <Steps
                  steps={[
                    "在上方开启 MCP。",
                    "打开 Claude Desktop，进入「设置 → 开发者 → 编辑配置」，会打开 claude_desktop_config.json。",
                    "把下面的内容合并进去（已有 mcpServers 时，只加 czlmail 这一项）。",
                    "保存后完全退出并重新打开 Claude Desktop，在对话框的工具列表里能看到 czlmail。",
                  ]}
                />
                <CodeBlock code={desktopConfig} />
                <p className="text-muted-foreground text-xs">
                  这种方式通过 CZL Mail 程序自带的桥接模式连接，不需要填写令牌；CZL Mail 没在运行时会被自动拉起。
                </p>
              </Card>
            </TabsContent>

            <TabsContent value="code" className="mt-3">
              <Card>
                <Steps steps={["在上方开启 MCP。", "在终端执行下面的命令（推荐，不需要令牌）："]} />
                <CodeBlock code={codeStdio} />
                <Steps start={3} steps={["或者用 HTTP 直连（端口或令牌变化后需要重新添加）："]} />
                {codeHttp ? <CodeBlock code={codeHttp} secret /> : <p className="text-muted-foreground text-sm">开启 MCP 后显示</p>}
                <Steps start={4} steps={["在 Claude Code 里输入 /mcp，能看到 czlmail 为已连接。"]} />
              </Card>
            </TabsContent>

            <TabsContent value="other" className="mt-3">
              <Card>
                <p className="text-sm">支持 MCP 的客户端（Cursor、Codex、Cherry Studio 等）任选一种方式：</p>
                <p className="text-muted-foreground flex items-center gap-1.5 text-xs">
                  <Terminal className="size-3.5" />
                  stdio：命令与参数
                </p>
                <CodeBlock code={`command: ${exe}\nargs: ["mcp"]`} />
                <p className="text-muted-foreground flex items-center gap-1.5 text-xs">
                  <KeyRound className="size-3.5" />
                  Streamable HTTP：地址与请求头
                </p>
                {status?.url ? (
                  <CodeBlock code={`URL: ${status.url}\nAuthorization: Bearer ${status.token}`} secret />
                ) : (
                  <p className="text-muted-foreground text-sm">开启 MCP 后显示</p>
                )}
              </Card>
            </TabsContent>
          </Tabs>
        </section>

        <section className="flex flex-col gap-3">
          <h2 className="flex items-center gap-2 text-sm font-semibold">
            <MessageSquareText className="size-4" />
            可以这样问
          </h2>
          <div className="grid gap-2 sm:grid-cols-2">
            {EXAMPLES.map((ex) => (
              <button
                key={ex}
                type="button"
                onClick={() => navigator.clipboard?.writeText(ex).then(() => toast.success("已复制"))}
                className="border-border hover:bg-secondary rounded-md border px-3 py-2.5 text-left text-sm"
              >
                {ex}
              </button>
            ))}
          </div>
        </section>

        <section className="flex flex-col gap-3">
          <h2 className="text-sm font-semibold">提供的工具</h2>
          <Card className="gap-0 p-0">
            {TOOLS.map((t, i) => (
              <div key={t.name} className={cn("flex items-center gap-3 px-4 py-2.5", i > 0 && "border-border border-t")}>
                <code className="bg-secondary w-36 shrink-0 rounded-sm px-1.5 py-0.5 font-mono text-xs">{t.name}</code>
                <span className="min-w-0 flex-1 text-sm">{t.desc}</span>
                <span
                  className={cn(
                    "shrink-0 rounded-sm px-1.5 py-0.5 text-xs",
                    t.write ? "bg-chart-5 text-foreground" : "text-muted-foreground",
                  )}
                >
                  {t.write ? "写入" : "只读"}
                </span>
              </div>
            ))}
          </Card>
        </section>

        <Card>
          <Row
            title="安全"
            desc="只接受本机连接，并以令牌鉴权。怀疑令牌泄露时可以更换；通过桥接方式连接的客户端不受影响。"
            icon={<ShieldAlert className="text-muted-foreground size-4" />}
          >
            <Button variant="outline" size="sm" disabled={!status?.enabled || busy} onClick={() => setConfirmReset(true)}>
              更换令牌
            </Button>
          </Row>
        </Card>
      </div>

      <AlertDialog open={confirmSend} onOpenChange={setConfirmSend}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>允许 AI 发送邮件？</AlertDialogTitle>
            <AlertDialogDescription>
              AI 读取的邮件可能来自任何人，其中可能夹带「把某某资料发到某地址」之类的指令。开启后，AI 客户端在调用发信工具前会征求你的同意，请务必看清收件人和内容再批准。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>保持关闭</AlertDialogCancel>
            <AlertDialogAction onClick={() => apply(true, true)}>我了解风险，开启</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={confirmReset} onOpenChange={setConfirmReset}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>更换令牌</AlertDialogTitle>
            <AlertDialogDescription>旧令牌立即失效，通过 HTTP 直连的客户端需要用新令牌重新配置。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction onClick={resetToken}>更换</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </ScrollArea>
  );
}

function Card({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn("border-border bg-card flex flex-col gap-3 rounded-md border p-4", className)}>{children}</div>;
}

function Divider() {
  return <div className="border-border border-t" />;
}

function Row({
  title,
  desc,
  icon,
  children,
}: {
  title: string;
  desc: string;
  icon?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-center gap-4">
      {icon}
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium">{title}</p>
        <p className="text-muted-foreground mt-0.5 text-xs leading-relaxed">{desc}</p>
      </div>
      <div className="flex shrink-0 items-center gap-2">{children}</div>
    </div>
  );
}

function Steps({ steps, start = 1 }: { steps: string[]; start?: number }) {
  return (
    <ol className="flex flex-col gap-1.5 text-sm" start={start}>
      {steps.map((s, i) => (
        <li key={i} className="flex gap-2">
          <span className="bg-secondary text-muted-foreground flex size-5 shrink-0 items-center justify-center rounded-full text-xs tabular-nums">
            {start + i}
          </span>
          <span className="leading-relaxed">{s}</span>
        </li>
      ))}
    </ol>
  );
}

/** 可复制的代码块。secret 为真时默认把令牌打码。 */
function CodeBlock({ code, secret }: { code: string; secret?: boolean }) {
  const [copied, setCopied] = useState(false);
  const [reveal, setReveal] = useState(!secret);
  const shown = reveal ? code : code.replace(/Bearer [0-9a-f]+/g, "Bearer ••••••••••••");

  return (
    <div className="bg-secondary group relative rounded-md">
      <pre className="overflow-x-auto p-3 pr-20 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all select-text">{shown}</pre>
      <div className="absolute top-1.5 right-1.5 flex gap-1">
        {secret && (
          <Button variant="ghost" size="icon" className="size-7" aria-label={reveal ? "隐藏令牌" : "显示令牌"} onClick={() => setReveal(!reveal)}>
            {reveal ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
          </Button>
        )}
        <Button
          variant="ghost"
          size="icon"
          className="size-7"
          aria-label="复制"
          onClick={() =>
            navigator.clipboard?.writeText(code).then(() => {
              setCopied(true);
              setTimeout(() => setCopied(false), 1500);
            })
          }
        >
          {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
        </Button>
      </div>
    </div>
  );
}
