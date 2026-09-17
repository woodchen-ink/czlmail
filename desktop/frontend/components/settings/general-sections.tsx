"use client";

import { useEffect, useState } from "react";
import { ExternalLink, Monitor, Moon, Sun } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { Card, Divider, NativeSelect, Row, SectionTitle, useSettings } from "@/components/settings/ui";
import { api, errorMessage, type Account, type IntegrationStatus } from "@/lib/api";
import { useAppearance, type ThemePreference } from "@/lib/theme";

export function AppearanceSection({ active }: { active: boolean }) {
  const { theme, setTheme, bodyAppearance, setBodyAppearance } = useAppearance();
  const { settings, save } = useSettings(active);

  return (
    <>
      <SectionTitle title="外观" />
      <Card>
        <Row title="主题" desc="跟随系统时，Windows 切换深浅色后界面会同步变化">
          <ToggleGroup type="single" variant="outline" size="sm" value={theme} onValueChange={(v) => v && setTheme(v as ThemePreference)}>
            <ToggleGroupItem value="system" aria-label="跟随系统">
              <Monitor className="size-4" />
            </ToggleGroupItem>
            <ToggleGroupItem value="light" aria-label="亮色">
              <Sun className="size-4" />
            </ToggleGroupItem>
            <ToggleGroupItem value="dark" aria-label="暗色">
              <Moon className="size-4" />
            </ToggleGroupItem>
          </ToggleGroup>
        </Row>
        <Divider />
        <Row title="邮件正文始终亮色" desc="多数营销与通知邮件按白底设计，暗色主题下也用白色纸面显示正文">
          <Switch checked={bodyAppearance === "light"} onCheckedChange={(on) => setBodyAppearance(on ? "light" : "follow")} />
        </Row>
        <Divider />
        <Row title="显示发件人头像" desc="头像由本程序代理获取（Gravatar、发件方网站图标），发件方看不到你的 IP；关闭后显示首字母">
          <Switch checked={!!settings?.senderAvatars} disabled={!settings} onCheckedChange={(on) => save({ senderAvatars: on })} />
        </Row>
      </Card>
    </>
  );
}

export function NotificationsSection({ active }: { active: boolean }) {
  const { settings, save } = useSettings(active);
  const [accounts, setAccounts] = useState<Account[]>([]);

  useEffect(() => {
    if (active)
      api
        .listAccounts()
        .then((list) => setAccounts(list ?? []))
        .catch(() => setAccounts([]));
  }, [active]);

  const muted = new Set(settings?.mutedMailAccounts ?? []);
  const mailOn = !!settings?.notifyMail;

  return (
    <>
      <SectionTitle title="通知" desc="使用系统通知中心。关闭主窗口后程序驻留托盘，仍会收到通知。" />
      <Card>
        <Row title="新邮件通知" desc="只通知收件箱里的来信；自己发出的邮件（任一发件身份）不通知。点击通知直接打开这封邮件">
          <Switch checked={mailOn} disabled={!settings} onCheckedChange={(on) => save({ notifyMail: on })} />
        </Row>
        <Divider />
        <Row title="日程提醒" desc="按日程上设置的提醒时间通知；在服务器上设置的提醒与其它设备一致">
          <Switch checked={!!settings?.notifyEvents} disabled={!settings} onCheckedChange={(on) => save({ notifyEvents: on })} />
        </Row>
      </Card>

      {accounts.length > 1 && (
        <>
          <SectionTitle title="按邮箱设置" desc="共享邮箱来信多时可以单独关闭，邮件照常同步，只是不弹通知。" small />
          <Card className="gap-0 p-0">
            {accounts.map((a, i) => (
              <div key={a.id} className={i > 0 ? "border-border border-t px-4 py-2.5" : "px-4 py-2.5"}>
                <Row title={a.name} desc={a.isPersonal ? "个人邮箱" : "共享邮箱"}>
                  <Switch
                    checked={mailOn && !muted.has(a.id)}
                    disabled={!settings || !mailOn}
                    onCheckedChange={(on) => {
                      const next = new Set(muted);
                      if (on) next.delete(a.id);
                      else next.add(a.id);
                      save({ mutedMailAccounts: [...next] });
                    }}
                  />
                </Row>
              </div>
            ))}
          </Card>
        </>
      )}
    </>
  );
}

export function IntegrationSection({ active }: { active: boolean }) {
  const [status, setStatus] = useState<IntegrationStatus | null>(null);

  useEffect(() => {
    if (active)
      api
        .getIntegrationStatus()
        .then(setStatus)
        .catch(() => setStatus(null));
  }, [active]);

  if (status && !status.supported) {
    return (
      <>
        <SectionTitle title="默认应用与启动" />
        <Card>
          <p className="text-muted-foreground text-sm">当前系统暂不支持这些设置。</p>
        </Card>
      </>
    );
  }

  return (
    <>
      <SectionTitle title="默认应用与启动" />
      <Card>
        <Row title="开机自动启动" desc="登录系统后在后台启动并驻留托盘，新邮件和日程提醒能及时送达">
          <Switch
            checked={!!status?.autostart}
            disabled={!status}
            onCheckedChange={async (on) => {
              try {
                setStatus(await api.setAutostart(on));
              } catch (err) {
                toast.error(errorMessage(err));
              }
            }}
          />
        </Row>
      </Card>
      <Card>
        <Row
          title="默认邮件应用"
          desc={status?.defaultMail ? "已是默认：点击网页上的邮箱链接会用 CZL Mail 写信" : "未设置：点击网页上的邮箱链接时不会打开 CZL Mail"}
        >
          <StatusDot on={!!status?.defaultMail} />
        </Row>
        <Divider />
        <Row
          title="默认日历应用"
          desc={status?.defaultCalendar ? "已是默认：双击 .ics 文件或订阅 webcal 链接会导入到日历" : "未设置：.ics 文件不会用 CZL Mail 打开"}
        >
          <StatusDot on={!!status?.defaultCalendar} />
        </Row>
        <Divider />
        <div className="flex items-center gap-3">
          <p className="text-muted-foreground flex-1 text-xs leading-relaxed">
            Windows 不允许程序自行修改默认应用。点击右侧按钮打开系统设置，在「CZL Mail」页面把 MAILTO、.ics、WEBCAL 设为 CZL Mail。
          </p>
          <Button
            variant="outline"
            size="sm"
            onClick={async () => {
              try {
                await api.openDefaultAppsSettings();
              } catch (err) {
                toast.error(errorMessage(err));
              }
            }}
          >
            <ExternalLink className="size-4" />
            打开系统设置
          </Button>
        </div>
      </Card>
    </>
  );
}

function StatusDot({ on }: { on: boolean }) {
  return <span className={on ? "text-chart-3 text-xs font-medium" : "text-muted-foreground text-xs"}>{on ? "已设置" : "未设置"}</span>;
}

export function ReadingSection({ active }: { active: boolean }) {
  const { settings, save } = useSettings(active);
  return (
    <>
      <SectionTitle title="阅读" />
      <Card>
        <Row title="标记为已读" desc="打开邮件后何时标记为已读">
          <NativeSelect
            value={settings?.markRead ?? "immediate"}
            disabled={!settings}
            onChange={(v) => save({ markRead: v })}
            options={[
              ["immediate", "立即"],
              ["delay", "停留 3 秒后"],
              ["never", "从不（手动标记）"],
            ]}
          />
        </Row>
      </Card>
    </>
  );
}

export function ComposeSection({ active }: { active: boolean }) {
  const { settings, save } = useSettings(active);
  return (
    <>
      <SectionTitle title="写信" desc="默认发件身份在「发件身份与签名」里设置。" />
      <Card>
        <Row title="签名前加分隔线" desc={'在签名前插入标准的 "-- " 行，收件方客户端可以识别并折叠签名'}>
          <Switch
            checked={!!settings?.signatureSeparator}
            disabled={!settings}
            onCheckedChange={(on) => save({ signatureSeparator: on })}
          />
        </Row>
        <Divider />
        <Row title="默认请求已读回执" desc="新邮件默认勾选「请求已读回执」，对方客户端是否回执由对方决定">
          <Switch
            checked={!!settings?.readReceiptDefault}
            disabled={!settings}
            onCheckedChange={(on) => save({ readReceiptDefault: on })}
          />
        </Row>
      </Card>
      <SectionTitle title="发送前检查" small />
      <Card>
        <Row title="主题为空时提醒">
          <Switch checked={!!settings?.warnEmptySubject} disabled={!settings} onCheckedChange={(on) => save({ warnEmptySubject: on })} />
        </Row>
        <Divider />
        <Row title="提到附件但没有添加时提醒" desc="正文或主题里出现「附件」「attached」等字样时检查">
          <Switch
            checked={!!settings?.warnMissingAttachment}
            disabled={!settings}
            onCheckedChange={(on) => save({ warnMissingAttachment: on })}
          />
        </Row>
      </Card>
    </>
  );
}
