"use client"

import * as React from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { AuthHeading, FormField, SubmitButton } from "@/components/auth-form"
import { authApi } from "@/lib/api/auth"
import { errorText } from "@/lib/errors"
import { useT } from "@/lib/i18n"
import { qk } from "@/lib/queries"

/** Only same-site admin paths are followed after sign-in. */
function safeNext(raw: string | null) {
  return raw && raw.startsWith("/admin/") && !raw.startsWith("//")
    ? raw
    : "/admin/"
}

function LoginForm() {
  const t = useT()
  const params = useSearchParams()
  const qc = useQueryClient()
  const [email, setEmail] = React.useState("")
  const [password, setPassword] = React.useState("")
  const [error, setError] = React.useState("")
  const setup = useQuery({ queryKey: qk.setup, queryFn: authApi.setupStatus })

  const login = useMutation({
    mutationFn: authApi.login,
    meta: { silent: true },
    onSuccess: (me) => {
      qc.setQueryData(qk.me, me)
      window.location.assign(safeNext(params.get("next")))
    },
    onError: (e) => setError(errorText(e)),
  })

  // No owner yet: this gateway needs its setup link first.
  const shown = error || (setup.data?.required ? t.auth.setupNoToken : "")

  return (
    <form
      className="grid gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        setError("")
        login.mutate({ email: email.trim(), password })
      }}
    >
      <AuthHeading title={t.auth.loginTitle} subtitle={t.auth.loginSubtitle} />
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
        id="password"
        label={t.auth.password}
        type="password"
        autoComplete="current-password"
        required
        value={password}
        onChange={(e) => setPassword(e.target.value)}
      />
      {shown && <p className="text-sm text-destructive">{shown}</p>}
      <SubmitButton pending={login.isPending} className="w-full">
        {login.isPending ? t.auth.loggingIn : t.auth.login}
      </SubmitButton>
      {setup.data?.registrationOpen && (
        <p className="text-center text-sm text-foreground-muted">
          {t.auth.noAccount}{" "}
          <Link href="/register/" className="text-link hover:underline">
            {t.auth.register}
          </Link>
        </p>
      )}
    </form>
  )
}

export default function LoginPage() {
  return (
    <React.Suspense>
      <LoginForm />
    </React.Suspense>
  )
}
