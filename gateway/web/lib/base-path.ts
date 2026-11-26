// Where the gateway serves the admin (next.config basePath). Links and router calls
// get it automatically; plain asset URLs (img src, manifest icons) need it added.
export const BASE_PATH = "/admin"

export function asset(path: string): string {
  return BASE_PATH + path
}
