// HTTP client for the gateway admin API (/api/admin/). Every response is
// { code, data, msg }; errors become ApiError with the numeric code.

export const API_BASE = "/api/admin"

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: number,
    readonly msg: string,
    readonly retryAfter?: number
  ) {
    super(`${code} ${msg}`)
    this.name = "ApiError"
  }

  /** The route does not exist yet (the backend work package has not landed). */
  get notReady(): boolean {
    return this.status === 404 && (this.code === 40400 || this.code === 404)
  }

  get unauthenticated(): boolean {
    return this.status === 401 && this.code === 40100
  }
}

export function isNotReady(err: unknown): boolean {
  return err instanceof ApiError && err.notReady
}

function csrfToken(): string {
  const m = document.cookie.match(/(?:^|;\s*)csrf_token=([^;]+)/)
  return m ? decodeURIComponent(m[1]) : ""
}

export async function request<T>(
  method: string,
  path: string,
  body?: unknown
): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" }
  const write = method !== "GET" && method !== "HEAD"
  if (write) {
    // The gateway refuses writes without a JSON body and the CSRF double-submit header.
    headers["Content-Type"] = "application/json"
    headers["X-CSRF-Token"] = csrfToken()
  }
  let res: Response
  try {
    res = await fetch(API_BASE + path, {
      method,
      headers,
      credentials: "same-origin",
      body: write ? JSON.stringify(body ?? {}) : undefined,
    })
  } catch {
    throw new ApiError(0, 0, "network_error")
  }
  let payload: { code?: number; data?: unknown; msg?: string } = {}
  try {
    payload = await res.json()
  } catch {
    // Not JSON: a proxy error page or an empty body.
  }
  const code =
    typeof payload.code === "number" ? payload.code : res.status * 100
  if (!res.ok || code !== 200) {
    const retry = Number(res.headers.get("Retry-After")) || undefined
    throw new ApiError(res.status, code, payload.msg ?? "error", retry)
  }
  return payload.data as T
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body?: unknown) => request<T>("POST", path, body),
  put: <T>(path: string, body?: unknown) => request<T>("PUT", path, body),
  patch: <T>(path: string, body?: unknown) => request<T>("PATCH", path, body),
  del: <T>(path: string) => request<T>("DELETE", path),
}
