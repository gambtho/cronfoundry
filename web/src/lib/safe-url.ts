/**
 * isSafeReviewURL guards the GitHub App permissions-review CTA against a
 * compromised backend injecting a `javascript:` href or a hostile redirect.
 * The 412 review_url should always be an https://github.com/... page.
 */
export function isSafeReviewURL(s: string): boolean {
  try {
    const u = new URL(s)
    return u.protocol === 'https:' && u.hostname === 'github.com'
  } catch {
    return false
  }
}
