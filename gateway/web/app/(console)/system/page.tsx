"use client"

import * as React from "react"

import { AuditTab } from "./components/audit-tab"
import { StatusTab } from "./components/status-tab"
import { TasksTab } from "./components/tasks-tab"
import { PageHeader } from "@/components/page-header"
import { Segmented } from "@/components/segmented"
import { useQueryParam } from "@/lib/hooks/use-query-param"
import { useT } from "@/lib/i18n"

const TABS = ["status", "tasks", "audit"] as const

function SystemPage() {
  const t = useT()
  const [tab, setTab] = useQueryParam<(typeof TABS)[number]>(
    "tab",
    TABS,
    "status"
  )
  return (
    <>
      <PageHeader
        title={t.system.title}
        subtitle={t.system.subtitle}
        actions={
          <Segmented
            value={tab}
            onChange={setTab}
            options={TABS.map((v) => ({ value: v, label: t.system.tabs[v] }))}
          />
        }
      />
      {tab === "status" && <StatusTab />}
      {tab === "tasks" && <TasksTab />}
      {tab === "audit" && <AuditTab />}
    </>
  )
}

export default function Page() {
  return (
    <React.Suspense>
      <SystemPage />
    </React.Suspense>
  )
}
