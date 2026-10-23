"use client"

import { useEffect } from "react"

/** Registers the no-op `sw.js` so iOS offers "Add to Home Screen"; it caches nothing. */
export function ServiceWorker() {
  useEffect(() => {
    if (!("serviceWorker" in navigator)) return
    navigator.serviceWorker.register("/sw.js", { scope: "/" }).catch((error) => console.error("service worker", error))
  }, [])
  return null
}
