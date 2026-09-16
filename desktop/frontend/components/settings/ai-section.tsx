"use client";

import { useEffect, useState } from "react";
import { CheckCircle2, Loader2, Sparkles } from "lucide-react";
import { toast } from "sonner";

import { Card, Divider, Row, SectionTitle } from "@/components/settings/ui";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { api, errorMessage, type AIConfig } from "@/lib/api";
import { main } from "@/wailsjs/go/models";

const LANGS = ["简体中文", "繁體中文", "English", "日本語", "한국어", "Deutsch", "Français", "Español", "Русский"];

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
        desc="接入兼容 OpenAI Responses API 的模型服务后，可以一键翻译邮件、润色和翻译草稿、按你的意图起草回复。邮件内容只发送到你配置的服务。"
      />
      <Card>
        <Row title="启用 AI 功能" desc={ready ? "已就绪：阅读邮件与写信时会显示 AI 按钮" : "填写接口信息并启用后，才会显示 AI 相关按钮"}>
          <Switch checked={cfg.enabled} disabled={saving} onCheckedChange={(on) => save({ enabled: on }, "")} />
        </Row>
      </Card>

      <Card>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="ai-base">接口地址（Base URL）</Label>
          <Input id="ai-base" value={cfg.baseUrl} onChange={(e) => update("baseUrl", e.target.value)} placeholder="https://api.openai.com/v1" />
          <p className="text-muted-foreground text-xs">会请求 {"{Base URL}"}/responses；填到 /v1 或只填域名都可以。</p>
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="ai-key">API Key</Label>
          <Input
            id="ai-key"
            type="password"
            autoComplete="off"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            placeholder={cfg.hasKey ? "已保存在系统钥匙串中，留空保持不变" : "sk-..."}
          />
          {cfg.hasKey && (
            <button type="button" className="text-muted-foreground hover:text-destructive self-start text-xs" onClick={() => save(undefined, "-")}>
              清除已保存的 Key
            </button>
          )}
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="ai-model">模型名称</Label>
          <Input id="ai-model" value={cfg.model} onChange={(e) => update("model", e.target.value)} placeholder="gpt-5-mini" />
        </div>
        <Divider />
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="ai-lang">默认翻译语言</Label>
          <Input id="ai-lang" list="ai-langs" value={cfg.translateLang} onChange={(e) => update("translateLang", e.target.value)} />
          <datalist id="ai-langs">
            {LANGS.map((l) => (
              <option key={l} value={l} />
            ))}
          </datalist>
          <p className="text-muted-foreground text-xs">阅读邮件时「翻译」按钮使用的目标语言。</p>
        </div>
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
      </Card>
    </>
  );
}
