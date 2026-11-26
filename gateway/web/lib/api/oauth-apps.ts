// OAuth apps the gateway signs in to Google and Microsoft with.
// TODO(#42 Google, #43 Microsoft): the routes land with those work packages.
import { api } from "./client"

export type OAuthProvider = "google" | "microsoft"

export interface OAuthApp {
  provider: OAuthProvider
  configured: boolean
  clientId: string
  redirectUri: string
  scopes: string[]
  /** Microsoft client secrets expire; Google's do not. */
  secretExpiresAt?: string | null
  tenant?: string
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
      "https://outlook.office.com/IMAP.AccessAsUser.All",
      "https://outlook.office.com/SMTP.Send",
      "openid",
      "email",
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
