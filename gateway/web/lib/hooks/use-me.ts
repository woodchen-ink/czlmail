"use client"

import { useQuery } from "@tanstack/react-query"

import { authApi } from "@/lib/api/auth"
import { qk } from "@/lib/queries"

/** The signed-in user; a 401 sends the browser to the sign-in page (see Providers). */
export function useMe() {
  return useQuery({
    queryKey: qk.me,
    queryFn: authApi.me,
    staleTime: 5 * 60_000,
    retry: false,
  })
}

export function isAdminRole(role: string | undefined) {
  return role === "owner" || role === "admin"
}
