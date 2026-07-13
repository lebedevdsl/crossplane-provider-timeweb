/*
Copyright 2026 Dmitry Lebedev.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cdn

import (
	"testing"
	"time"

	cdnv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/cdn/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
)

func i64(v int64) *int64   { return &v }
func str(v string) *string { return &v }
func idn(cn string, domains []string, day string) *certIdentity {
	return &certIdentity{cn: cn, domains: domains, expiryDay: day}
}

var (
	uploadedCert = timeweb.CDNCertificate{ID: 1, Type: "uploaded", CN: "a.example.com",
		Domains: []string{"a.example.com"}, ExpiresAt: "2026-10-10T12:00:00Z"}
	leCert = timeweb.CDNCertificate{ID: 2, Type: "lets_encrypt", CN: "a.example.com",
		Domains: []string{"a.example.com"}, ExpiresAt: "2026-10-11T12:00:00Z"}
	markerUploaded = &cdnv1alpha1.CdnCertificateStatus{ID: i64(1), Type: str("uploaded"),
		CN: str("a.example.com"), ExpiresAt: str("2026-10-10T12:00:00Z")}
)

func TestComputeSSLTable(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	old := now.Add(-20 * time.Minute)
	recent := now.Add(-5 * time.Minute)
	desired := idn("a.example.com", []string{"a.example.com"}, "2026-10-10")

	cases := []struct {
		name       string
		in         sslInputs
		wantKind   sslActionKind
		wantState  string
		wantCertID int64
	}{
		{"custom: no match → upload",
			sslInputs{mode: "custom", desired: desired, now: now},
			sslActionUpload, sslStatePending, 0},
		{"custom: no match, prior upload of SAME identity failed → no re-upload",
			sslInputs{mode: "custom", desired: desired, prevState: sslStateFailed,
				budgetKey: "k", prevBudgetKey: "k", now: now},
			sslActionNone, sslStateFailed, 0},
		{"custom: no match, identity changed (Secret rotated) → upload again",
			sslInputs{mode: "custom", desired: desired, prevState: sslStateFailed,
				budgetKey: "new", prevBudgetKey: "old", now: now},
			sslActionUpload, sslStatePending, 0},
		{"custom: match unbound → bind",
			sslInputs{mode: "custom", desired: desired, certificates: []timeweb.CDNCertificate{uploadedCert}, now: now},
			sslActionBind, sslStatePending, 1},
		{"custom: match bound → bound, adopt marker",
			sslInputs{mode: "custom", desired: desired, boundID: i64(1),
				certificates: []timeweb.CDNCertificate{uploadedCert}, now: now},
			sslActionNone, sslStateBound, 0},
		{"custom: bound + duplicate of same identity → delete the duplicate, keep bound",
			sslInputs{mode: "custom", desired: desired, boundID: i64(1),
				certificates: []timeweb.CDNCertificate{uploadedCert,
					{ID: 2, Type: "uploaded", CN: "a.example.com", Domains: []string{"a.example.com"}, ExpiresAt: "2026-10-10T12:00:00Z"}},
				now: now},
			sslActionDeleteOld, sslStateBound, 2},
		{"custom rotation: new bound, old managed present → delete old",
			sslInputs{mode: "custom", desired: idn("a.example.com", []string{"a.example.com"}, "2026-12-01"),
				boundID: i64(3), managed: markerUploaded,
				certificates: []timeweb.CDNCertificate{uploadedCert,
					{ID: 3, Type: "uploaded", CN: "a.example.com", Domains: []string{"a.example.com"}, ExpiresAt: "2026-12-01T12:00:00Z"}},
				now: now},
			sslActionDeleteOld, sslStateBound, 1},
		{"custom rotation: old id REUSED by different identity → no delete, adopt current",
			sslInputs{mode: "custom", desired: idn("a.example.com", []string{"a.example.com"}, "2026-12-01"),
				boundID: i64(3), managed: markerUploaded,
				certificates: []timeweb.CDNCertificate{
					{ID: 1, Type: "lets_encrypt", CN: "other.example.com", Domains: []string{"other.example.com"}, ExpiresAt: "2026-11-11T12:00:00Z"},
					{ID: 3, Type: "uploaded", CN: "a.example.com", Domains: []string{"a.example.com"}, ExpiresAt: "2026-12-01T12:00:00Z"}},
				now: now},
			sslActionNone, sslStateBound, 0},
		{"le: materialized unbound → bind (defensive; upstream auto-binds)",
			sslInputs{mode: "letsEncrypt", certificates: []timeweb.CDNCertificate{leCert}, now: now},
			sslActionBind, sslStatePending, 2},
		{"le: materialized bound → bound",
			sslInputs{mode: "letsEncrypt", boundID: i64(2), certificates: []timeweb.CDNCertificate{leCert}, now: now},
			sslActionNone, sslStateBound, 0},
		{"le: declared domains not attached → wait (issue after aliases)",
			sslInputs{mode: "letsEncrypt", domainsAttached: false, attempts: 0, now: now},
			sslActionNone, sslStatePending, 0},
		{"le: domains attached + budget ok → issue",
			sslInputs{mode: "letsEncrypt", domainsAttached: true, attempts: 1, lastAttempt: &old, now: now},
			sslActionIssue, sslStatePending, 0},
		{"le: task in_progress → issuing, no action",
			sslInputs{mode: "letsEncrypt",
				tasks: []timeweb.CDNCertificateTask{{ID: 9, Status: "in_progress"}}, now: now},
			sslActionNone, sslStateIssuing, 0},
		{"le: failed tombstones only + budget ok + spaced → issue",
			sslInputs{mode: "letsEncrypt", domainsAttached: true,
				tasks:    []timeweb.CDNCertificateTask{{ID: 8, Status: "failed"}},
				attempts: 1, lastAttempt: &old, now: now},
			sslActionIssue, sslStatePending, 0},
		{"le: within spacing window (attempts>0) → wait",
			sslInputs{mode: "letsEncrypt", domainsAttached: true, attempts: 1, lastAttempt: &recent, now: now},
			sslActionNone, sslStateFailed, 0},
		{"le: fresh budget (attempts==0) ignores stale timestamp → issue now",
			sslInputs{mode: "letsEncrypt", domainsAttached: true, attempts: 0, lastAttempt: &recent, now: now},
			sslActionIssue, sslStatePending, 0},
		{"le: budget spent → exhausted",
			sslInputs{mode: "letsEncrypt", domainsAttached: true, attempts: 4, lastAttempt: &old, now: now},
			sslActionNone, sslStateExhausted, 0},
		{"le: budget key rotated → attempts reset → issue",
			sslInputs{mode: "letsEncrypt", domainsAttached: true, attempts: 4, lastAttempt: &old,
				budgetKey: "new", prevBudgetKey: "old", now: now},
			sslActionIssue, sslStatePending, 0},
		{"le: budget key rotated bypasses stale spacing window → issue now",
			sslInputs{mode: "letsEncrypt", domainsAttached: true, attempts: 0, lastAttempt: &recent,
				budgetKey: "new", prevBudgetKey: "old", now: now},
			sslActionIssue, sslStatePending, 0},
		{"le: OUR non-LE cert occupies slot → unbind first (409 guard)",
			sslInputs{mode: "letsEncrypt", boundID: i64(1), managed: markerUploaded,
				certificates: []timeweb.CDNCertificate{uploadedCert}, now: now},
			sslActionUnbind, sslStatePending, 0},
		{"le: FOREIGN cert occupies slot → blocked, no delete",
			sslInputs{mode: "letsEncrypt", boundID: i64(1),
				certificates: []timeweb.CDNCertificate{uploadedCert}, now: now},
			sslActionNone, sslStateFailed, 0},
		{"none: bound → unbind",
			sslInputs{mode: "none", boundID: i64(1),
				certificates: []timeweb.CDNCertificate{uploadedCert}, now: now},
			sslActionUnbind, sslStatePending, 0},
		{"none: unbound + managed present → delete ours",
			sslInputs{mode: "none", managed: markerUploaded,
				certificates: []timeweb.CDNCertificate{uploadedCert}, now: now},
			sslActionDeleteOld, sslStatePending, 1},
		{"none: unbound + id reused by foreign identity → no delete, marker cleared",
			sslInputs{mode: "none", managed: markerUploaded,
				certificates: []timeweb.CDNCertificate{{ID: 1, Type: "lets_encrypt", CN: "x.example.com",
					Domains: []string{"x.example.com"}, ExpiresAt: "2027-01-01T00:00:00Z"}}, now: now},
			sslActionNone, "", 0},
		{"absent block → unowned",
			sslInputs{mode: "", now: now},
			sslActionNone, "", 0},
	}

	for _, c := range cases {
		out := computeSSL(c.in)
		if out.action.kind != c.wantKind {
			t.Fatalf("%s: action = %v, want %v", c.name, out.action.kind, c.wantKind)
		}
		if out.state != c.wantState {
			t.Fatalf("%s: state = %q, want %q", c.name, out.state, c.wantState)
		}
		if c.wantCertID != 0 && out.action.certID != c.wantCertID {
			t.Fatalf("%s: certID = %d, want %d", c.name, out.action.certID, c.wantCertID)
		}
		if c.name == "none: unbound + id reused by foreign identity → no delete, marker cleared" && out.managed != nil {
			t.Fatalf("%s: expected marker cleared", c.name)
		}
	}
}

func TestParseTLSCertificateAndIdentity(t *testing.T) {
	// Self-signed cert generated once for the test corpus (CN=a.example.com,
	// SAN a.example.com). Parsing failures on garbage must be clean errors.
	if _, err := parseTLSCertificate([]byte("not a pem")); err == nil {
		t.Fatal("expected error on garbage PEM")
	}
	id := identityOf(uploadedCert)
	if id.cn != "a.example.com" || id.expiryDay != "2026-10-10" {
		t.Fatalf("identityOf readback mapping wrong: %+v", id)
	}
	if !managedMatches(markerUploaded, uploadedCert) {
		t.Fatal("marker must match its own certificate")
	}
	stranger := uploadedCert
	stranger.Type = "lets_encrypt"
	if managedMatches(markerUploaded, stranger) {
		t.Fatal("marker must NOT match same id with different identity (id reuse)")
	}
}

func TestSSLBudgetKeyChangesOnSpecEdit(t *testing.T) {
	ssl := &cdnv1alpha1.CdnSSL{Mode: "letsEncrypt"}
	a := sslBudgetKey([]string{"a.example.com"}, ssl, nil)
	b := sslBudgetKey([]string{"b.example.com"}, ssl, nil)
	c := sslBudgetKey([]string{"a.example.com"}, &cdnv1alpha1.CdnSSL{Mode: "custom"}, nil)
	if a == b || a == c {
		t.Fatalf("budget key must change on domains/ssl edits: %s %s %s", a, b, c)
	}
	if a != sslBudgetKey([]string{"a.example.com"}, ssl, nil) {
		t.Fatal("budget key must be deterministic")
	}
	d := sslBudgetKey([]string{"a.example.com"}, ssl, idn("a.example.com", []string{"a.example.com"}, "2026-10-10"))
	e := sslBudgetKey([]string{"a.example.com"}, ssl, idn("a.example.com", []string{"a.example.com"}, "2026-12-31"))
	if d == e || d == a {
		t.Fatal("certificate identity (rotation) must rotate the budget key")
	}
}
