// OAuth apps the gateway signs in to Google and Microsoft with. GET lists only
// the providers this gateway build supports (Microsoft arrives with #43).
import { api } from "./client"

export type OAuthProvider = "google" | "microsoft"

export interface OAuthApp {
  provider: OAuthProvider
  configured: boolean
  clientId: string
  /** A client secret is stored (it is never sent back). */
  hasSecret: boolean
  redirectUri: string
  scopes: string[]
  /** Microsoft client secrets expire; Google's do not. */
  secretExpiresAt?: string | null
  tenant?: string
  /** From secretExpiresAt: expiring = within 30 days. */
  secretState: "unknown" | "ok" | "expiring" | "expired"
  /**
   * Microsoft: the tenant-wide consent link, once a client ID is stored. A tenant
   * admin who opens it comes back to /admin/mailboxes/?adminConsent=granted.
   */
  adminConsentUrl?: string
  updatedAt: string | null
}

export interface OAuthAppInput {
  clientId: string
  clientSecret?: string
  tenant?: string
  secretExpiresAt?: string
}

/**
 * Used for display while the backend does not answer; the wizard shows the
 * scopes GET /oauth-apps returns. Google: only the Gmail scope (the address
 * comes from the Gmail profile, not from openid / email).
 */
export const OAUTH_DEFAULTS: Record<
  OAuthProvider,
  { callbackPath: string; scopes: string[] }
> = {
  google: {
    callbackPath: "/oauth/google/callback",
    scopes: ["https://mail.google.com/"],
  },
  microsoft: {
    callbackPath: "/oauth/microsoft/callback",
    scopes: [
      "offline_access",
      "openid",
      "email",
      "https://outlook.office.com/IMAP.AccessAsUser.All",
      "https://outlook.office.com/SMTP.Send",
    ],
  },
}

export const oauthAppsApi = {
  list: () => api.get<OAuthApp[]>("/oauth-apps"),
  save: (provider: OAuthProvider, body: OAuthAppInput) =>
    api.put<OAuthApp>(`/oauth-apps/${provider}`, body),
  remove: (provider: OAuthProvider) =>
    api.del<object>(`/oauth-apps/${provider}`),
}
