"use client"

import * as React from "react"

/**
 * Current time, refreshed every interval. 0 in the static HTML and during hydration,
 * so both renders agree; relative times appear once mounted.
 */
export function useNow(intervalMs = 30_000): number {
  const subscribe = React.useCallback(
    (onChange: () => void) => {
      const id = setInterval(onChange, intervalMs)
      return () => clearInterval(id)
    },
    [intervalMs]
  )
  // Rounded to the interval so getSnapshot is stable between ticks.
  return React.useSyncExternalStore(
    subscribe,
    () => Math.floor(Date.now() / intervalMs) * intervalMs,
    () => 0
  )
}
