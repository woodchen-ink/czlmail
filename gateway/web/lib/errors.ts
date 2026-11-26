import { ApiError } from "@/lib/api/client"
import { t } from "@/lib/i18n"

/** User-facing text for an API error, always with its numeric code. */
export function errorText(err: unknown): string {
  const m = t()
  if (err instanceof ApiError) {
    if (err.code === 0) return m.errors.network
    if (err.notReady) return m.common.notReadyTitle
    return m.errors.withCode(
      m.errors.codes[err.code] ?? m.errors.generic,
      err.code
    )
  }
  return m.errors.generic
}
