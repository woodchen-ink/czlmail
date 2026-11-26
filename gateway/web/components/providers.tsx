"use client"

import * as React from "react"
import {
  MutationCache,
  QueryCache,
  QueryClient,
  QueryClientProvider,
} from "@tanstack/react-query"
import { toast } from "sonner"

import { ThemeProvider } from "@/components/theme-provider"
import { Toaster } from "@/components/ui/sonner"
import { TooltipProvider } from "@/components/ui/tooltip"
import { I18nProvider } from "@/components/i18n-provider"
import { ApiError } from "@/lib/api/client"
import { errorText } from "@/lib/errors"

/** Sends the browser to the sign-in page when the session is gone. */
function toLogin() {
  const here = window.location.pathname + window.location.search
  if (window.location.pathname.startsWith("/admin/login")) return
  window.location.assign(`/admin/login/?next=${encodeURIComponent(here)}`)
}

function makeClient() {
  return new QueryClient({
    queryCache: new QueryCache({
      onError: (err) => {
        if (err instanceof ApiError && err.unauthenticated) toLogin()
      },
    }),
    mutationCache: new MutationCache({
      onError: (err, _vars, _ctx, mutation) => {
        if (err instanceof ApiError && err.unauthenticated) return toLogin()
        // A mutation can handle its own errors with meta: { silent: true }.
        if (!mutation.meta?.silent) toast.error(errorText(err))
      },
    }),
    defaultOptions: {
      queries: {
        staleTime: 30_000,
        gcTime: 5 * 60_000,
        refetchOnWindowFocus: false,
        retry: (count, err) => {
          if (
            err instanceof ApiError &&
            (err.status === 401 ||
              err.status === 403 ||
              err.status === 404 ||
              err.status === 429)
          )
            return false
          return count < 1
        },
      },
    },
  })
}

export function Providers({ children }: { children: React.ReactNode }) {
  const [client] = React.useState(makeClient)
  return (
    <ThemeProvider>
      <QueryClientProvider client={client}>
        <TooltipProvider delayDuration={300}>
          <I18nProvider>{children}</I18nProvider>
        </TooltipProvider>
        <Toaster position="top-center" richColors={false} />
      </QueryClientProvider>
    </ThemeProvider>
  )
}
