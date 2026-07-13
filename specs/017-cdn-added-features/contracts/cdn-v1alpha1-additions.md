# Contract: Cdn v1alpha1 additions (017)

Additive to specs/016-cdn-resource/contracts/cdn-v1alpha1.md.

Admission: domains MaxItems=2 + subdomain pattern; ssl.certificateSecretRef
required iff ssl.mode=custom (forbidden otherwise); origin.awsAuthSecretRef
forbidden with origin.bucketRef; trafficLimitGBPerMonth ≥ 1.

Annotations: `cdn.timeweb.crossplane.io/retry-ssl` (self-clearing budget reset).

Conditions/states: `status.atProvider.ssl.state ∈ pending | issuing | bound |
failed | exhausted`; Ready is NEVER gated by SSL state (resource serves via the
technical domain regardless); Events: `SSLIssuanceFailed` (no upstream reason —
quirk), `SSLBudgetExhausted`, `CertificateRemoved`.

Secret hygiene: TLS private key, signing key, AWS keys — never in status,
Events, or logs; configuration reads remain secret-bearing.

LE-unverified warning (docs + release notes): letsEncrypt mode implemented per
the captured contract but never observed succeeding upstream (ticket filed
2026-07-13); custom certificates are the verified path.
