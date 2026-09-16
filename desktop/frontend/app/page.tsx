"use client";

import { useCallback, useEffect, useState } from "react";
import { Loader2 } from "lucide-react";

import { AppShell } from "@/components/app-shell";
import { SignIn } from "@/components/sign-in";
import { Events, api, hasRuntime, onEvent } from "@/lib/api";

type Phase = "loading" | "signed-out" | "signed-in" | "no-runtime";

export default function Page() {
  const [phase, setPhase] = useState<Phase>("loading");

  const refresh = useCallback(async () => {
    if (!hasRuntime()) {
      setPhase("no-runtime");
      return;
    }
    try {
      const status = await api.getSessionStatus();
      // configured 而非 connected: 配置还在但网络暂时不通时，
      // 应当进入邮件界面读本地缓存，而不是把用户赶回登录页。
      setPhase(status.configured ? "signed-in" : "signed-out");
    } catch {
      setPhase("signed-out");
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  useEffect(() => {
    const off = onEvent(Events.connected, () => setPhase("signed-in"));
    return off;
  }, []);

  if (phase === "loading") {
    return (
      <div className="text-muted-foreground flex h-full items-center justify-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" />
        启动中
      </div>
    );
  }

  if (phase === "no-runtime") {
    return (
      <div className="flex h-full items-center justify-center p-6">
        <p className="text-muted-foreground max-w-sm text-center text-sm">
          需要在桌面应用中运行。开发时请使用 <code className="font-mono">wails dev</code>，
          直接打开浏览器页面无法访问本地邮件数据。
        </p>
      </div>
    );
  }

  if (phase === "signed-out") {
    return <SignIn onSignedIn={refresh} />;
  }

  return <AppShell onSignedOut={() => setPhase("signed-out")} />;
}
