"use client"

import * as React from "react"
import Link from "next/link"
import { useMutation, useQueryClient } from "@tanstack/react-query"

import { AuthHeading, FormField, SubmitButton } from "@/components/auth-form"
import { authApi } from "@/lib/api/auth"
import { errorText } from "@/lib/errors"
import { useT } from "@/lib/i18n"
import { qk } from "@/lib/queries"

export default function RegisterPage() {
  const t = useT()
  const qc = useQueryClient()
  const [email, setEmail] = React.useState("")
  const [name, setName] = React.useState("")
  const [password, setPassword] = React.useState("")
  const [error, setError] = React.useState("")
  const register = useMutation({
    mutationFn: authApi.register,
    meta: { silent: true },
    onSuccess: (me) => {
      qc.setQueryData(qk.me, me)
      window.location.assign("/admin/")
    },
    onError: (e) => setError(errorText(e)),
  })

  return (
    <form
      className="grid gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        setError("")
        if (password.length < 10) return setError(t.errors.codes[40002])
        register.mutate({ email: email.trim(), name: name.trim(), password })
      }}
    >
      <AuthHeading title={t.auth.registerTitle} />
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
      <SubmitButton pending={register.isPending} className="w-full">
        {t.auth.registerSubmit}
      </SubmitButton>
      <p className="text-center text-sm text-foreground-muted">
        {t.auth.haveAccount}{" "}
        <Link href="/login/" className="text-link hover:underline">
          {t.auth.login}
        </Link>
      </p>
    </form>
  )
}
