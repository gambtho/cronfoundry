/**
 * ApiError carries the structured error JSON envelope our backend returns.
 *
 * `code` is the machine-readable enum (e.g. "permission_required",
 * "validation"). `extras` holds any additional fields the backend included
 * (most notably `review_url` for 412).
 *
 * Plain `Error.message` is set to the human-readable error string for
 * backwards-compat with existing `try/catch (e)` consumers.
 */
export class ApiError extends Error {
  status: number
  code: string
  extras: Record<string, unknown>

  constructor(message: string, status: number, code: string, extras: Record<string, unknown> = {}) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.extras = extras
  }
}

/** Type guard — the ApiError class crosses bundler boundaries cleanly. */
export function isApiError(e: unknown): e is ApiError {
  return e instanceof ApiError
}
