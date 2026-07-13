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
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cdnv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/cdn/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
)

// RetrySSLAnnotation resets the Let's Encrypt retry budget (self-clearing —
// the purge-annotation idiom).
const RetrySSLAnnotation = "cdn.timeweb.crossplane.io/retry-ssl"

// SSL lifecycle states surfaced in status.atProvider.ssl.state.
const (
	sslStatePending   = "pending"
	sslStateIssuing   = "issuing"
	sslStateBound     = "bound"
	sslStateFailed    = "failed"
	sslStateExhausted = "exhausted"
)

// LE retry budget: attempts spaced >= issueSpacing, at most maxIssueAttempts
// per budget window (inside LE's own failed-validation rate limit). Reset via
// a domains/ssl spec change (budget key rotation) or RetrySSLAnnotation.
const (
	maxIssueAttempts = 4
	issueSpacing     = 15 * time.Minute
)

// Event reasons for the certificate lifecycle.
const (
	eventSSLIssuanceFailed  = "SSLIssuanceFailed"
	eventSSLBudgetExhausted = "SSLBudgetExhausted"
	eventCertificateRemoved = "CertificateRemoved"
	eventSSLUploadFailed    = "SSLUploadFailed"
)

// sslActionKind is the single certificate action a reconcile may perform.
type sslActionKind int

const (
	sslActionNone sslActionKind = iota
	sslActionIssue
	sslActionUpload
	sslActionBind
	sslActionUnbind
	sslActionDeleteOld
)

type sslAction struct {
	kind   sslActionKind
	certID int64 // bind / deleteOld target
}

// certIdentity is the comparable identity of a certificate: parsed locally
// from the Secret's PEM, or taken from the upstream readback (the platform
// parses uploads the same way). Expiry is compared at day granularity — the
// readback truncates seconds asymmetrically.
type certIdentity struct {
	cn        string
	domains   []string // sorted
	expiryDay string   // YYYY-MM-DD (UTC)
}

func (a certIdentity) equal(b certIdentity) bool {
	return a.cn == b.cn && a.expiryDay == b.expiryDay && slicesEqual(a.domains, b.domains)
}

// parseTLSCertificate extracts the identity from the first PEM block of
// tls.crt. The PEM content itself is secret-adjacent — never logged.
func parseTLSCertificate(crtPEM []byte) (certIdentity, error) {
	block, _ := pem.Decode(crtPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return certIdentity{}, fmt.Errorf("tls.crt does not start with a PEM CERTIFICATE block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return certIdentity{}, fmt.Errorf("parse tls.crt: %w", err)
	}
	domains := append([]string(nil), cert.DNSNames...)
	if len(domains) == 0 && cert.Subject.CommonName != "" {
		domains = []string{cert.Subject.CommonName}
	}
	sort.Strings(domains)
	return certIdentity{
		cn:        cert.Subject.CommonName,
		domains:   domains,
		expiryDay: cert.NotAfter.UTC().Format("2006-01-02"),
	}, nil
}

func identityOf(c timeweb.CDNCertificate) certIdentity {
	domains := append([]string(nil), c.Domains...)
	sort.Strings(domains)
	day := c.ExpiresAt
	if len(day) >= 10 {
		day = day[:10]
	}
	return certIdentity{cn: c.CN, domains: domains, expiryDay: day}
}

// sslInputs is everything computeSSL needs (pure function — unit-testable).
type sslInputs struct {
	mode            string // "", none, letsEncrypt, custom ("" = block absent)
	desired         *certIdentity
	boundID         *int64                            // config.security.certificate_id
	managed         *cdnv1alpha1.CdnCertificateStatus // identity-checked owner marker
	certificates    []timeweb.CDNCertificate
	tasks           []timeweb.CDNCertificateTask
	domainsAttached bool // declared delivery domains all present in aliases
	budgetKey       string
	prevBudgetKey   string
	attempts        int64
	lastAttempt     *time.Time
	now             time.Time
}

// sslOutcome carries the chosen action plus the status bookkeeping to mirror.
type sslOutcome struct {
	action   sslAction
	state    string
	attempts int64
	managed  *cdnv1alpha1.CdnCertificateStatus
}

// managedMatches reports whether an inventory certificate is THE one the
// marker describes: id AND identity (type, cn, expiry day) must all agree —
// upstream reuses ids, so id alone must never authorize a delete.
func managedMatches(m *cdnv1alpha1.CdnCertificateStatus, c timeweb.CDNCertificate) bool {
	if m == nil || m.ID == nil || *m.ID != c.ID {
		return false
	}
	if m.Type != nil && *m.Type != c.Type {
		return false
	}
	if m.CN != nil && *m.CN != c.CN {
		return false
	}
	if m.ExpiresAt != nil && len(*m.ExpiresAt) >= 10 && len(c.ExpiresAt) >= 10 &&
		(*m.ExpiresAt)[:10] != c.ExpiresAt[:10] {
		return false
	}
	return true
}

func markerFor(c timeweb.CDNCertificate) *cdnv1alpha1.CdnCertificateStatus {
	id, typ, cn, exp := c.ID, c.Type, c.CN, c.ExpiresAt
	return &cdnv1alpha1.CdnCertificateStatus{ID: &id, Type: &typ, CN: &cn, ExpiresAt: &exp}
}

// computeSSL derives ONE action per reconcile from the declared mode and the
// observed upstream state. Ordering rule (wire-verified): a MATERIALIZED
// certificate wins over any task state — successful tasks vanish from the
// task list, so failed tombstones coexist with success.
func computeSSL(in sslInputs) sslOutcome {
	out := sslOutcome{attempts: in.attempts, managed: in.managed}
	if in.budgetKey != in.prevBudgetKey {
		out.attempts = 0 // spec/Secret change restarts the budget; the first
		// attempt of a fresh budget (attempts==0) always fires immediately.
	}

	switch in.mode {
	case "custom":
		if in.desired == nil { // secret unresolved — caller surfaced the error
			out.state = sslStateFailed
			return out
		}
		var match *timeweb.CDNCertificate
		for i := range in.certificates {
			if identityOf(in.certificates[i]).equal(*in.desired) {
				match = &in.certificates[i]
				break
			}
		}
		if match == nil {
			// Uploads share the issue budget: spacing + cap prevent a
			// terminally-rejected certificate (untrusted root, …) from being
			// re-sent every reconcile and starving the settings PATCH
			// (live-gate finding 2026-07-13). Budget resets on spec/Secret
			// change (identity is part of the budget key) or retry-ssl.
			if out.attempts >= maxIssueAttempts {
				out.state = sslStateExhausted
				return out
			}
			if out.attempts > 0 && in.lastAttempt != nil && in.now.Sub(*in.lastAttempt) < issueSpacing {
				out.state = sslStateFailed
				return out
			}
			out.state = sslStatePending
			out.action = sslAction{kind: sslActionUpload}
			return out
		}
		if in.boundID == nil || *in.boundID != match.ID {
			out.state = sslStatePending
			out.action = sslAction{kind: sslActionBind, certID: match.ID}
			return out
		}
		out.state = sslStateBound
		if out.managed == nil { // we uploaded it last reconcile
			out.managed = markerFor(*match)
		}
		// Rotation leftovers: delete OUR old certificate (id+identity match)
		// once the new one is bound.
		if out.managed.ID != nil && *out.managed.ID != match.ID {
			for i := range in.certificates {
				if managedMatches(out.managed, in.certificates[i]) {
					out.action = sslAction{kind: sslActionDeleteOld, certID: in.certificates[i].ID}
					return out
				}
			}
			out.managed = markerFor(*match) // old gone (or id reused by a stranger) — adopt current
		}
		return out

	case "letsEncrypt":
		var le *timeweb.CDNCertificate
		for i := range in.certificates {
			if in.certificates[i].Type == "lets_encrypt" {
				le = &in.certificates[i]
				break
			}
		}
		// A non-LE certificate occupying the slot blocks issuance upstream
		// (409 Conflict, wire-verified): clear OUR cert first; a foreign one
		// is never deleted — the operator must remove it.
		if le == nil {
			if in.boundID != nil {
				for i := range in.certificates {
					if in.certificates[i].ID == *in.boundID && in.certificates[i].Type != "lets_encrypt" {
						if managedMatches(out.managed, in.certificates[i]) {
							out.state = sslStatePending
							out.action = sslAction{kind: sslActionUnbind}
							return out
						}
						out.state = sslStateFailed // foreign cert occupies the slot
						return out
					}
				}
			}
			if out.managed != nil {
				for i := range in.certificates {
					if managedMatches(out.managed, in.certificates[i]) && in.certificates[i].Type != "lets_encrypt" {
						out.state = sslStatePending
						out.action = sslAction{kind: sslActionDeleteOld, certID: in.certificates[i].ID}
						return out
					}
				}
			}
		}
		if le != nil { // materialized — tasks are irrelevant now
			if in.boundID == nil || *in.boundID != le.ID {
				out.state = sslStatePending
				out.action = sslAction{kind: sslActionBind, certID: le.ID} // defensive: upstream auto-binds
				return out
			}
			out.state = sslStateBound
			if out.managed == nil {
				out.managed = markerFor(*le)
			}
			return out
		}
		if t := latestTask(in.tasks); t != nil && t.Status == "in_progress" {
			out.state = sslStateIssuing
			return out
		}
		// LE issues for the resource's delivery domains — the declared custom
		// domains must be attached (aliases) FIRST. Defer to let the settings
		// PATCH add them (live-gate ordering, 2026-07-13).
		if !in.domainsAttached {
			out.state = sslStatePending
			return out
		}
		if out.attempts >= maxIssueAttempts {
			out.state = sslStateExhausted
			return out
		}
		if out.attempts > 0 && in.lastAttempt != nil && in.now.Sub(*in.lastAttempt) < issueSpacing {
			out.state = sslStateFailed // waiting out the spacing window
			return out
		}
		out.state = sslStatePending
		out.action = sslAction{kind: sslActionIssue}
		return out

	case "none":
		if in.boundID != nil {
			out.state = sslStatePending
			out.action = sslAction{kind: sslActionUnbind}
			return out
		}
		if out.managed != nil {
			for i := range in.certificates {
				if managedMatches(out.managed, in.certificates[i]) {
					out.action = sslAction{kind: sslActionDeleteOld, certID: in.certificates[i].ID}
					out.state = sslStatePending
					return out
				}
			}
			out.managed = nil // gone, or its id was reused by a certificate we did not create
		}
		out.state = ""
		return out

	default: // block absent — unowned
		out.state = ""
		return out
	}
}

func latestTask(tasks []timeweb.CDNCertificateTask) *timeweb.CDNCertificateTask {
	var latest *timeweb.CDNCertificateTask
	for i := range tasks {
		if latest == nil || tasks[i].ID > latest.ID {
			latest = &tasks[i]
		}
	}
	return latest
}

// sslBudgetKey fingerprints the declaration whose change resets the budget —
// domains, ssl mode, and (for custom) the certificate identity, so rotating
// the Secret restarts a spent budget.
func sslBudgetKey(domains []string, ssl *cdnv1alpha1.CdnSSL, desired *certIdentity) string {
	h := fnv.New64a()
	ds := append([]string(nil), domains...)
	sort.Strings(ds)
	_, _ = h.Write([]byte(strings.Join(ds, ",")))
	if ssl != nil {
		_, _ = h.Write([]byte("|" + ssl.Mode))
	}
	if desired != nil {
		_, _ = h.Write([]byte("|" + desired.cn + "|" + strings.Join(desired.domains, ",") + "|" + desired.expiryDay))
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

// listCertificates / listTasks — SSL reads, only issued when the block is owned.
func (e *external) listCertificates(ctx context.Context, resourceID string) ([]timeweb.CDNCertificate, error) {
	var env struct {
		Certificates []timeweb.CDNCertificate `json:"certificates"`
	}
	if err := doJSON(func() (*http.Response, error) { return e.tw.ListCDNCertificates(ctx, resourceID) }, &env); err != nil {
		return nil, err
	}
	return env.Certificates, nil
}

func (e *external) listCertificateTasks(ctx context.Context, resourceID string) ([]timeweb.CDNCertificateTask, error) {
	var env struct {
		Tasks []timeweb.CDNCertificateTask `json:"certificate_tasks"`
	}
	if err := doJSON(func() (*http.Response, error) { return e.tw.ListCDNCertificateTasks(ctx, resourceID) }, &env); err != nil {
		return nil, err
	}
	return env.Tasks, nil
}

// executeSSLAction performs the single chosen action. Issue attempts update
// the budget bookkeeping on the CR (persisted via the status update).
func (e *external) executeSSLAction(ctx context.Context, cr *cdnv1alpha1.Cdn, action sslAction, certPEM, keyPEM string, resourceID int64) error {
	id := strconv.FormatInt(action.certID, 10)
	switch action.kind {
	case sslActionUpload:
		now := metav1.Now()
		ensureSSLStatus(cr)
		attempts := int64(1)
		if cr.Status.AtProvider.SSL.IssueAttempts != nil {
			attempts = *cr.Status.AtProvider.SSL.IssueAttempts + 1
		}
		cr.Status.AtProvider.SSL.IssueAttempts = &attempts
		cr.Status.AtProvider.SSL.LastIssueAttemptAt = &now
		err := e.do(func() (*http.Response, error) {
			return e.tw.UploadCDNCertificate(ctx, certPEM, keyPEM, resourceID)
		})
		if err != nil && !errors.Is(err, timeweb.ErrTransient) {
			// Terminal rejection (e.g. 422 cert_add_root_not_trusted: the
			// chain must end in a system-trusted root — self-signed refused,
			// live-verified). Surfacing + settling into the failed state
			// beats hammering an upload the platform will never accept; a
			// Secret/spec change re-triggers naturally.
			e.event(cr, "Warning", eventSSLUploadFailed, err.Error())
			ensureSSLStatus(cr)
			st := sslStateFailed
			cr.Status.AtProvider.SSL.State = &st
			return nil
		}
		return err
	case sslActionIssue:
		now := metav1.Now()
		ensureSSLStatus(cr)
		prev := int64(0)
		if cr.Status.AtProvider.SSL.IssueAttempts != nil {
			prev = *cr.Status.AtProvider.SSL.IssueAttempts
		}
		attempts := prev + 1
		cr.Status.AtProvider.SSL.IssueAttempts = &attempts
		cr.Status.AtProvider.SSL.LastIssueAttemptAt = &now
		err := e.do(func() (*http.Response, error) { return e.tw.IssueCDNCertificate(ctx, resourceID) })
		if err != nil {
			// 409 cert_issue_task_already_exists: an issuance task is still
			// tracked server-side (even when the visible list shows only
			// failed tombstones — wire-verified) ⇒ issuance is in flight, not
			// a failure: no budget charge, no warning.
			if strings.Contains(err.Error(), "cert_issue_task_already_exists") {
				cr.Status.AtProvider.SSL.IssueAttempts = &prev
				e.event(cr, "Normal", eventSSLIssuanceFailed, "issuance task already in flight upstream; waiting")
				return nil
			}
			// 422 cert_issue_incorrect_dns and friends: warn + wait for the
			// next budget slot — never a reconcile error (Qrator discipline).
			e.event(cr, "Warning", eventSSLIssuanceFailed, err.Error())
			if attempts >= maxIssueAttempts {
				e.event(cr, "Warning", eventSSLBudgetExhausted,
					fmt.Sprintf("Let's Encrypt retry budget spent (%d attempts); fix DNS and annotate %s to retry", attempts, RetrySSLAnnotation))
			}
			return nil
		}
		return nil
	case sslActionBind:
		return e.do(func() (*http.Response, error) {
			return e.tw.PatchCDNHTTPResource(ctx, strconv.FormatInt(resourceID, 10), timeweb.CDNResourceWrite{
				Config: &timeweb.CDNConfigPatch{Security: &timeweb.CDNConfigSecurityPatch{CertificateID: timeweb.JSONValue(action.certID)}},
			})
		})
	case sslActionUnbind:
		return e.do(func() (*http.Response, error) {
			return e.tw.PatchCDNHTTPResource(ctx, strconv.FormatInt(resourceID, 10), timeweb.CDNResourceWrite{
				Config: &timeweb.CDNConfigPatch{Security: &timeweb.CDNConfigSecurityPatch{CertificateID: timeweb.JSONNull}},
			})
		})
	case sslActionDeleteOld:
		err := e.do(func() (*http.Response, error) { return e.tw.DeleteCDNCertificate(ctx, id) })
		if err == nil {
			e.event(cr, "Normal", eventCertificateRemoved, "removed managed certificate "+id)
			cr.Status.AtProvider.ManagedCertificate = nil
		}
		return err // 409 certificate_in_use = transient → retried after unbind lands
	}
	return nil
}

func ensureSSLStatus(cr *cdnv1alpha1.Cdn) {
	if cr.Status.AtProvider.SSL == nil {
		cr.Status.AtProvider.SSL = &cdnv1alpha1.CdnSSLStatus{}
	}
}
