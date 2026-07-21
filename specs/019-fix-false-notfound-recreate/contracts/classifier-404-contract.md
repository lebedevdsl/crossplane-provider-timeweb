# Contract: `Classify` 404 branch

**Unit under test**: `func Classify(resp *http.Response) error` in
`internal/clients/timeweb/errors.go`.

## Contract

Given `resp.StatusCode == 404`:

1. The body is read once and decoded as the Timeweb error envelope (`errorResponseBody`).
2. **If** decode succeeds AND `error_code` is non-empty (canonical envelope present) →
   return the `ErrNotFound` sentinel, wrapped with the existing message/`response_id` detail
   when available. (FR-002, FR-003)
3. **Else** (empty body, non-JSON/HTML, or JSON without `error_code`) → return a
   `*TransientError` whose `StatusCode` is 404 and whose `Reason` names the cause
   ("404 without canonical error envelope — suspected upstream flap"). `errors.Is(result,
   ErrNotFound)` MUST be **false**; `errors.Is(result, ErrTransient)` MUST be **true**.
   (FR-001, FR-007)

All non-404 behavior is unchanged (FR-006).

## Test cases (errors_test.go)

| # | Input body (Content-Type) | Assert |
|---|---------------------------|--------|
| C1 | `{"status_code":404,"error_code":"not_found","message":"Resource not found","response_id":"abc"}` | `errors.Is(err, ErrNotFound)` true; message + `response_id` present in `err.Error()` |
| C2 | `` (empty) | `errors.Is(err, ErrTransient)` true; `errors.Is(err, ErrNotFound)` false |
| C3 | `<html>404 Not Found</html>` (text/html) | `errors.Is(err, ErrTransient)` true; not ErrNotFound |
| C4 | `{"foo":"bar"}` (json, no error_code) | `errors.Is(err, ErrTransient)` true; not ErrNotFound |
| C5 | `{"error_code":"not_found"}` (minimal envelope) | `errors.Is(err, ErrNotFound)` true |
| C6 | regression: existing 2xx/5xx/409/other-4xx cases | unchanged results |

## Guard contract (notfound_guard_test.go)

Scanning `internal/controller/**/*.go` (non-test):

- MUST fail if a controller file references `http.StatusNotFound` or a raw `== 404` status
  comparison, or maps a raw HTTP status to `ResourceExists: false`.
- MUST pass for the current tree (all controllers route through
  `errors.Is(err, timeweb.ErrNotFound)` / `errors.Is(err, rgwiam.ErrNoSuchEntity)`).
- Excludes generated client, `errors.go`, and `rgwiam` (they legitimately inspect status).
- (FR-004, FR-012, FR-009 — non-Timeweb canonical signals remain allowed.)
