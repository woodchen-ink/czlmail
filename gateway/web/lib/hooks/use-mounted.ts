"use client"

import * as React from "react"

const noop = () => () => {}

/** False in the static HTML and during hydration, true afterwards. */
export function useMounted(): boolean {
  return React.useSyncExternalStore(
    noop,
    () => true,
    () => false
  )
}
