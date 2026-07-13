# Contract: CDN certificates endpoints (captured 2026-07-13, resource 22209)

- `GET /api/v1/cdn/certificates?resource_id=<id>` → `{certificates: [{id, type: "uploaded"|"lets_encrypt", cn, domains: [SANs], issued_at, expires_at}]}`
- `GET /api/v1/cdn/certificates/tasks?resource_id=<id>` → `{certificate_tasks:
  [{id, status: "in_progress"|"failed"|<success-unobserved>, domains,
  resource_id}]}` — tasks ACCUMULATE (history); key on max(id); failed tasks
  carry NO reason (quirk, ticket filed).
- `POST /api/v1/cdn/certificates` `{certificate: "<PEM chain>", private_key:
  "<PEM>"}` → 204 (no id returned — discover via inventory). EXACTLY these two
  fields (chain MUST end in a system-trusted root — self-signed → **422
  cert_add_root_not_trusted**, live gate 2026-07-13): adding `resource_id` → 400 "property resource_id should not exist"
  (live gate 2026-07-13); certificates are account-scoped until bound, yet
  still appear under `GET /certificates?resource_id=` pre-bind.
- `POST /api/v1/cdn/certificates/issue` body `{"resource_id": <id>}`
  (payload CAPTURED 2026-07-13 — no query param) → 202 (async task) with live
  CNAME; **422 cert_issue_incorrect_dns** without; **409
  cert_issue_main_domain_in_use** ("Main domain already in use") when a
  certificate already covers the domain — free the slot first.
- Bind/unbind: resource PATCH `{config: {security: {certificate_id: <id>|null}}}`;
  `certificate_id` echoed by the configuration read.
- `DELETE /api/v1/cdn/certificates/<id>` → **409 {"error_code":
  "certificate_in_use", details: {resource_id, resource_name}}** while bound
  (transient-classified ⇒ self-healing order), 204 after unbind.
- `POST /certificates/issue` while a certificate already exists →
  **409 cert_issue_main_domain_in_use** (captured 2026-07-13) — the slot must be freed
  (unbind + delete) before switching to LE; the controller sequences this and
  never issues into an occupied slot. A foreign (non-managed) occupying cert
  blocks the switch with a clear Event instead of a delete.
- LE issuance SUCCEEDED on the 3rd attempt (2026-07-13) after two reasonless
  failures — root cause almost certainly DNS propagation to LE's resolvers.
  Readback: `{id: 1, type: "lets_encrypt", cn, domains, issued_at, expires_at}`;
  HTTPS through the custom domain verified manually. NOTE: the LE cert took
  id 1 — the same id the (deleted) uploaded cert had ⇒ certificate ids are
  per-resource / reused; managedCertificateID comparisons are valid only
  within one resource AND must be invalidated when the id's identity (type/cn)
  no longer matches what we created. Still unobserved: task success-state
  string, auto-bind behavior — implementation detects success by
  materialization and binds-if-unbound defensively.
