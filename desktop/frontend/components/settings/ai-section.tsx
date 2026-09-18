"use client";

import { useEffect, useState } from "react";
import { CheckCircle2, Loader2, Sparkles } from "lucide-react";
import { toast } from "sonner";

import { Card, Row, SectionTitle } from "@/components/settings/ui";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { api, errorMessage, type AIConfig } from "@/lib/api";
import { main } from "@/wailsjs/go/models";

const LANGS = ["简体中文", "繁體中文", "English", "日本語", "한국어", "Deutsch", "Français", "Español", "Русский"];

// 思考档位(reasoning.effort)。档位名一律用接口里的原词 —— 模型的报错也是这么写的
// (「该模型始终思考，不支持关闭思考；请使用 low、high 或 max」), 译成中文反而对不上。
// auto 不是接口值, 表示不带这个参数。
const EFFORTS = ["auto", "none", "low", "medium", "high", "xhigh", "max"];

/** 0.1.18 短暂用过 off 表示"不带这个参数", 现在叫 auto。 */
function effortValue(v: string): string {
  return !v || v === "off" ? "auto" : v;
}

/** 标题固定宽度, 两行的按钮组左边缘才对得齐。 */
function EffortRow({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
      <p className="w-28 shrink-0 text-sm font-medium">{title}</p>
      {children}
    </div>
  );
}

function EffortChoice({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <ToggleGroup
      type="single"
      variant="outline"
      size="sm"
      value={effortValue(value)}
      onValueChange={(v) => v && onChange(v)}
      className="flex-wrap justify-start"
    >
      {EFFORTS.map((e) => (
        <ToggleGroupItem key={e} value={e} className="font-mono">
          {e}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  );
}

/**
 * AI 接入: 兼容 OpenAI Responses API(/v1/responses)的服务。
 * API Key 只存系统钥匙串, 界面上不回显。
 */
export function AISection({ active }: { active: boolean }) {
  const [cfg, setCfg] = useState<AIConfig | null>(null);
  const [key, setKey] = useState("");
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState("");

  useEffect(() => {
    if (active)
      api
        .getAIConfig()
        .then(setCfg)
        .catch((err) => toast.error(errorMessage(err)));
  }, [active]);

  function update<K extends keyof AIConfig>(k: K, v: AIConfig[K]) {
    setCfg((c) => (c ? main.AIConfig.createFrom({ ...c, [k]: v }) : c));
  }

  async function save(patch?: Partial<AIConfig>, apiKey = key.trim()) {
    if (!cfg) return;
    const next = main.AIConfig.createFrom({ ...cfg, ...patch });
    setSaving(true);
    try {
      const saved = await api.saveAIConfig(next, apiKey);
      setCfg(saved);
      setKey("");
      if (!patch) toast.success("AI 设置已保存");
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  async function test() {
    setTesting(true);
    setTestResult("");
    try {
      if (key.trim() || cfg) await save(undefined);
      const reply = await api.testAI();
      setTestResult(reply || "连接成功");
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setTesting(false);
    }
  }

  if (!cfg) {
    return (
      <>
        <SectionTitle title="AI 助手" />
        <Card>
          <div className="text-muted-foreground flex items-center justify-center gap-2 py-6 text-sm">
            <Loader2 className="size-4 animate-spin" />
            载入中
          </div>
        </Card>
      </>
    );
  }

  const ready = cfg.enabled && cfg.hasKey && !!cfg.baseUrl && !!cfg.model;

  return (
    <>
      <SectionTitle
        title="AI 助手"
        desc="接入兼容 OpenAI Responses API 的模型服务后，可以翻译邮件、润色和翻译草稿、按你的意图起草回复。邮件内容只发送到你配置的服务。"
      />
      <Card>
        <Row title="启用 AI 功能" desc={ready ? "已就绪：阅读邮件与写信时会显示 AI 按钮" : "填好接口信息并启用后，才会显示 AI 相关按钮"}>
          <Switch checked={cfg.enabled} disabled={saving} onCheckedChange={(on) => save({ enabled: on }, "")} />
        </Row>
      </Card>

      <Card>
        <div className="grid gap-3 sm:grid-cols-3">
          <div className="flex flex-col gap-1.5 sm:col-span-2">
            <Label htmlFor="ai-base">接口地址</Label>
            <Input id="ai-base" value={cfg.baseUrl} onChange={(e) => update("baseUrl", e.target.value)} placeholder="https://api.openai.com/v1" />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ai-model">模型名称</Label>
            <Input id="ai-model" value={cfg.model} onChange={(e) => update("model", e.target.value)} placeholder="gpt-5-mini" />
          </div>
          <div className="flex flex-col gap-1.5 sm:col-span-2">
            <Label htmlFor="ai-key">API Key</Label>
            <Input
              id="ai-key"
              type="password"
              autoComplete="off"
              value={key}
              onChange={(e) => setKey(e.target.value)}
              placeholder={cfg.hasKey ? "已存入系统钥匙串，留空保持不变" : "sk-..."}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ai-lang">翻译语言</Label>
            <Input id="ai-lang" list="ai-langs" value={cfg.translateLang} onChange={(e) => update("translateLang", e.target.value)} />
            <datalist id="ai-langs">
              {LANGS.map((l) => (
                <option key={l} value={l} />
              ))}
            </datalist>
          </div>
        </div>
        <p className="text-muted-foreground text-xs">
          会请求 {"{接口地址}"}/responses，填到 /v1 或只填域名都可以。
          {cfg.hasKey && (
            <button type="button" className="hover:text-destructive ml-2 underline underline-offset-2" onClick={() => save(undefined, "-")}>
              清除已保存的 Key
            </button>
          )}
        </p>
      </Card>

      <Card>
        <SectionTitle
          small
          title="思考档位"
          desc="auto 不带这个参数，由模型自己决定；none 要求完全不思考，模型关不掉时自动改为不带。档位用接口原词。"
        />
        <EffortRow title="翻译">
          <EffortChoice value={cfg.reasoningTranslate} onChange={(v) => update("reasoningTranslate", v)} />
        </EffortRow>
        <EffortRow title="润色、起草回复">
          <EffortChoice value={cfg.reasoningWrite} onChange={(v) => update("reasoningWrite", v)} />
        </EffortRow>
      </Card>

      <div className="flex items-center justify-end gap-2">
        {testResult && (
          <span className="text-muted-foreground mr-auto flex min-w-0 items-center gap-1.5 text-xs">
            <CheckCircle2 className="text-chart-3 size-4 shrink-0" />
            <span className="truncate">{testResult}</span>
          </span>
        )}
        <Button variant="outline" onClick={test} disabled={testing || saving}>
          {testing ? <Loader2 className="size-4 animate-spin" /> : <Sparkles className="size-4" />}
          测试连接
        </Button>
        <Button onClick={() => save()} disabled={saving}>
          {saving && <Loader2 className="size-4 animate-spin" />}
          保存
        </Button>
      </div>
    </>
  );
}
