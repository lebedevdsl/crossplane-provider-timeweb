# Quickstart: CDN Added Features (017)

```yaml
spec:
  forProvider:
    origin:
      domain: bucket.external-s3.example
      https: true
      awsAuthSecretRef: { name: ext-s3-keys }      # external private S3 only
    domains: [cdn.example.com, static.example.com] # ≤2; CNAME → technicalDomain (operator DNS)
    ssl:
      mode: custom                                 # none | letsEncrypt | custom
      certificateSecretRef: { name: example-tls }  # kubernetes.io/tls
    security:
      forceHTTPS: true
      secureToken:
        secretRef: { name: cdn-signing }           # key "secret" by default
        restrictByIP: true
    trafficLimitGBPerMonth: 3000
```

- **letsEncrypt mode: ⚠ UNVERIFIED against the live platform** — issuance
  currently fails upstream with no reason even with a correct CNAME (ticket
  filed 2026-07-13). Bounded retries (4 × ≥15 min), then `ssl.state: exhausted`;
  reset with a spec edit or `kubectl annotate cdn/X
  cdn.timeweb.crossplane.io/retry-ssl=now`. Custom certificates are the
  verified path.
- Custom cert rotation: update the TLS Secret — the provider re-uploads,
  rebinds, and removes its old certificate automatically.
- Signed URLs: app-side, `urlsafe_b64(md5(secret+path+ip+expires))` →
  `https://<domain>/md5(<token>,<expires>)/<path>` (see docs/cdn.md).

| Symptom | Cause / action |
|---|---|
| `ssl.state: pending` + DNS event | CNAME not live yet — fix DNS, auto-retries |
| `ssl.state: exhausted` | retry budget spent — fix cause, then retry-ssl annotation |
| cert delete retried with 409 events | unbind hadn't landed — self-heals, no action |
| resource Suspended after limit | raise/clear trafficLimitGBPerMonth (up to 2 h upstream lag, traffic bills meanwhile) |
