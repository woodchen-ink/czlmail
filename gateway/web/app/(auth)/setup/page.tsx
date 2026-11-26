"use client"

import * as React from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { AuthHeading, FormField, SubmitButton } from "@/components/auth-form"
import { Button } from "@/components/ui/button"
import { authApi } from "@/lib/api/auth"
import { errorText } from "@/lib/errors"
import { useT } from "@/lib/i18n"
import { qk } from "@/lib/queries"

function SetupForm() {
  const t = useT()
  const params = useSearchParams()
  const qc = useQueryClient()
  const token = params.get("token") ?? ""
  const status = useQuery({ queryKey: qk.setup, queryFn: authApi.setupStatus })
  const [email, setEmail] = React.useState("")
  const [name, setName] = React.useState("")
  const [password, setPassword] = React.useState("")
  const [error, setError] = React.useState("")

  const setup = useMutation({
    mutationFn: authApi.setup,
    meta: { silent: true },
    onSuccess: (me) => {
      qc.setQueryData(qk.me, me)
      window.location.assign("/admin/")
    },
    onError: (e) => setError(errorText(e)),
  })

  if (status.data && !status.data.required) {
    return (
      <div className="grid gap-4">
        <AuthHeading title={t.auth.setupTitle} subtitle={t.auth.setupDone} />
        <Button asChild>
          <Link href="/login/">{t.auth.goLogin}</Link>
        </Button>
      </div>
    )
  }

  return (
    <form
      className="grid gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        setError("")
        if (password.length < 10) return setError(t.errors.codes[40002])
        setup.mutate({
          token,
          email: email.trim(),
          name: name.trim(),
          password,
        })
      }}
    >
      <AuthHeading title={t.auth.setupTitle} subtitle={t.auth.setupSubtitle} />
      {!token && (
        <p className="rounded-md border border-warning-border bg-warning-soft px-3 py-2 text-sm text-warning">
          {t.auth.setupNoToken}
        </p>
      )}
      <FormField
        id="email"
        label={t.auth.email}
        type="email"
        autoComplete="username"
        required
        value={email}
        onChange={(e) => setEmail(e.target.value)}
      />
      <FormField
        id="name"
        label={t.auth.name}
        placeholder={t.auth.namePlaceholder}
        value={name}
        onChange={(e) => setName(e.target.value)}
      />
      <FormField
        id="password"
        label={t.auth.password}
        type="password"
        autoComplete="new-password"
        required
        minLength={10}
        hint={t.auth.passwordHint}
        value={password}
        onChange={(e) => setPassword(e.target.value)}
      />
      {error && <p className="text-sm text-destructive">{error}</p>}
      <SubmitButton
        pending={setup.isPending}
        disabled={!token}
        className="w-full"
      >
        {t.auth.setupSubmit}
      </SubmitButton>
      <p className="text-xs text-foreground-subtle">{t.auth.setupHint}</p>
    </form>
  )
}

export default function SetupPage() {
  return (
    <React.Suspense>
      <SetupForm />
    </React.Suspense>
  )
}
