"use client"

import * as React from "react"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Spinner } from "@/components/spinner"

export function AuthHeading({
  title,
  subtitle,
}: {
  title: string
  subtitle?: string
}) {
  return (
    <div className="mb-5">
      <h1 className="text-lg font-semibold">{title}</h1>
      {subtitle && (
        <p className="mt-1 text-sm text-foreground-muted">{subtitle}</p>
      )}
    </div>
  )
}

export function FormField({
  id,
  label,
  hint,
  error,
  ...props
}: React.ComponentProps<typeof Input> & {
  id: string
  label: string
  hint?: string
  error?: string
}) {
  return (
    <div className="grid gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} aria-invalid={!!error || undefined} {...props} />
      {error ? (
        <p className="text-xs text-destructive">{error}</p>
      ) : hint ? (
        <p className="text-xs text-foreground-subtle">{hint}</p>
      ) : null}
    </div>
  )
}

export function SubmitButton({
  pending,
  children,
  ...props
}: React.ComponentProps<typeof Button> & { pending?: boolean }) {
  return (
    <Button type="submit" disabled={pending || props.disabled} {...props}>
      {pending && <Spinner />}
      {children}
    </Button>
  )
}
