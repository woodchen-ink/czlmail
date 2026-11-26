"use client"

import * as React from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"

/**
 * A view state kept in the URL query (tab, range, section), so reload and sharing
 * return to the same view. Invalid values fall back to the default.
 */
export function useQueryParam<T extends string>(
  name: string,
  allowed: readonly T[],
  fallback: T
): [T, (v: T) => void] {
  const params = useSearchParams()
  const router = useRouter()
  const pathname = usePathname()
  const raw = params.get(name)
  const value = (allowed as readonly string[]).includes(raw ?? "")
    ? (raw as T)
    : fallback
  const set = React.useCallback(
    (v: T) => {
      const next = new URLSearchParams(params.toString())
      if (v === fallback) next.delete(name)
      else next.set(name, v)
      const qs = next.toString()
      router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false })
    },
    [params, router, pathname, name, fallback]
  )
  return [value, set]
}
