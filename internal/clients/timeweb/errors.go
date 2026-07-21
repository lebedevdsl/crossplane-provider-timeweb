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

package timeweb

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrNotFound is returned when the Timeweb API replies with HTTP 404. Callers
// (managed-resource external clients) MUST inspect for this sentinel via
// errors.Is so that Observe can report ResourceExists=false without flapping
// the Synced condition.
var ErrNotFound = errors.New("timeweb: resource not found")

// APIError is a terminal error carrying the upstream HTTP status, error code
// (if present in the response body), and a human-readable message. The
// reconciler should surface .Error() on the CR's Synced condition.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

// Error implements error.
func (e *APIError) Error() string {
	if e == nil {
		return "<nil timeweb.APIError>"
	}
	if e.Code != "" {
		return fmt.Sprintf("timeweb api: %d %s: %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("timeweb api: %d: %s", e.StatusCode, e.Message)
}

// TransientError wraps a retryable failure (5xx, 408, 409, 425, 429, network).
// Callers should requeue without flipping Synced=False. errors.Is(err, ErrTransient)
// returns true on every TransientError.
type TransientError struct {
	StatusCode int // 0 for non-HTTP transient errors (network, DNS, …)
	Reason     string
	Underlying error
}

// Error implements error.
func (e *TransientError) Error() string {
	if e == nil {
		return "<nil timeweb.TransientError>"
	}
	if e.Underlying != nil {
		return fmt.Sprintf("timeweb transient (%s): %s", e.Reason, e.Underlying)
	}
	return fmt.Sprintf("timeweb transient (%s)", e.Reason)
}

// Unwrap exposes the underlying error for errors.Unwrap callers.
func (e *TransientError) Unwrap() error { return e.Underlying }

// ErrTransient is the sentinel returned by errors.Is(err, ErrTransient).
var ErrTransient = errors.New("timeweb: transient error")

// Is implements errors.Is so callers can branch on transience without a type
// assertion: `errors.Is(err, timeweb.ErrTransient)` is the supported idiom.
func (e *TransientError) Is(target error) bool { return target == ErrTransient }

// errorResponseBody is the canonical Timeweb error envelope (status_code,
// error_code, message, response_id). Anonymous oneOf message fields are
// decoded into interface{} per the generator patch in the Makefile.
type errorResponseBody struct {
	StatusCode any    `json:"status_code"`
	ErrorCode  string `json:"error_code"`
	Message    any    `json:"message"`
	ResponseID string `json:"response_id"`
}

// Classify converts a Timeweb HTTP response into a Go error following the
// rules documented in research.md §R-3:
//
//	200, 201, 204                 → nil (success)
//	404                           → ErrNotFound (sentinel)
//	408, 409, 425, 429, 5xx       → *TransientError
//	other 4xx                     → *APIError (terminal)
//
// On a nil response or an error returned by the HTTP layer (network, DNS,
// context deadline) the caller should classify via ClassifyNetworkError.
func Classify(resp *http.Response) error {
	if resp == nil {
		return &TransientError{Reason: "nil response"}
	}

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusNotFound:
		// A resource is "deleted" ONLY on a canonical, precisely-classified
		// not-found — never on the HTTP status alone. A genuine Timeweb 404
		// carries the documented error envelope (`not-found` response schema:
		// status_code/error_code/response_id are required); an edge/Qrator/
		// gateway 404 arrives as HTML or an empty body with no envelope.
		// Treating a bare 404 as deleted recreated a live VPC (postmortem #124:
		// Observe→ResourceExists:false→Create), so require the envelope here.
		// Absent envelope ⇒ suspected upstream flap ⇒ transient ⇒ requeue,
		// NEVER recreation. The envelope's `error_code` is the discriminator;
		// when present, surface the upstream message + response_id (Constitution
		// §II: never swallow the upstream explanation), wrapping ErrNotFound so
		// callers that tolerate 404 on delete (errors.Is) still match.
		code, detail := readNotFoundEnvelope(resp)
		if code == "" {
			return &TransientError{
				StatusCode: http.StatusNotFound,
				Reason:     "404 without canonical error envelope (suspected upstream flap; not treating as deleted)",
			}
		}
		if detail != "" {
			return fmt.Errorf("%w: %s", ErrNotFound, detail)
		}
		return ErrNotFound
	case isTransientStatus(resp.StatusCode):
		reason := http.StatusText(resp.StatusCode)
		// Surface the upstream explanation (don't swallow it — Constitution §II).
		// e.g. a 409 on cluster create carries the actual conflict reason.
		if msg := readErrorMessage(resp); msg != "" {
			reason = reason + ": " + msg
		}
		return &TransientError{
			StatusCode: resp.StatusCode,
			Reason:     reason,
		}
	default:
		err := decodeAPIError(resp)
		// 403 networks_location_mismatch is a settle-delay, not a denial:
		// attaching a seconds-old VPC to a router returns this code even
		// with matching zones and succeeds ~1 min later once the VPC settles
		// (feature-006 probe, 2026-06-11). Retry instead of surfacing a
		// terminal Synced=False.
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden && apiErr.Code == "networks_location_mismatch" {
			return &TransientError{
				StatusCode: apiErr.StatusCode,
				Reason:     "Forbidden (networks_location_mismatch — newly created VPCs settle in ~1 min): " + apiErr.Message,
			}
		}
		return err
	}
}

// ClassifyNetworkError wraps a transport-level error (returned by http.Client.Do
// before any HTTP response was produced) into a transient error so callers
// requeue rather than surface Synced=False.
func ClassifyNetworkError(err error) error {
	if err == nil {
		return nil
	}
	return &TransientError{Reason: "network", Underlying: err}
}

func isTransientStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout,
		// 409 stays transient DELIBERATELY: the upstream returns it during
		// async cleanup windows — e.g. a router-network detach lingers for a
		// few seconds and a follow-up write 409s until it completes
		// (feature-006 probe). A blanket reclassification to terminal would
		// break that retry path; genuinely-terminal conflicts (duplicate
		// names, …) are rare and self-describe in the surfaced reason.
		http.StatusConflict,
		http.StatusTooEarly,
		http.StatusTooManyRequests:
		return true
	}
	return code >= 500 && code < 600
}

func decodeAPIError(resp *http.Response) error {
	apiErr := &APIError{StatusCode: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil || len(body) == 0 {
		return apiErr
	}

	var b errorResponseBody
	if err := json.Unmarshal(body, &b); err == nil {
		if b.ErrorCode != "" {
			apiErr.Code = b.ErrorCode
		}
		if m := stringifyMessage(b.Message); m != "" {
			apiErr.Message = m
		}
	}
	return apiErr
}

// readErrorMessage best-effort extracts the upstream error message from a
// response body (JSON `message` oneOf<string,[]string>, falling back to a raw
// snippet). Returns "" when nothing useful is present. Used to enrich both
// transient and terminal error reasons so the upstream explanation is never
// silently dropped.
func readErrorMessage(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil || len(body) == 0 {
		return ""
	}
	var b errorResponseBody
	if err := json.Unmarshal(body, &b); err == nil {
		if m := stringifyMessage(b.Message); m != "" {
			return m
		}
	}
	// Not the expected JSON shape — return a trimmed raw snippet.
	if s := strings.TrimSpace(string(body)); s != "" {
		if len(s) > 300 {
			s = s[:300]
		}
		return s
	}
	return ""
}

// readNotFoundEnvelope reads the response body once and reports the canonical
// Timeweb error envelope for a 404: the `error_code` (empty when the body is
// NOT the canonical JSON envelope — HTML edge page, empty body, or JSON lacking
// error_code) and a formatted message+response_id detail for enrichment. A
// non-empty code is the signal that a 404 is a genuine not-found rather than an
// edge/gateway flap; the message names the missing resource and the response_id
// correlates with a Timeweb support ticket. See Classify's 404 branch (#124).
func readNotFoundEnvelope(resp *http.Response) (code, detail string) {
	if resp == nil || resp.Body == nil {
		return "", ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil || len(body) == 0 {
		return "", ""
	}
	var b errorResponseBody
	// Not the canonical JSON envelope (e.g. an HTML edge page) ⇒ no code ⇒ the
	// caller treats it as a suspected flap, not a deletion.
	if err := json.Unmarshal(body, &b); err != nil {
		return "", ""
	}
	msg := stringifyMessage(b.Message)
	switch {
	case msg != "" && b.ResponseID != "":
		detail = fmt.Sprintf("%s (response_id: %s)", msg, b.ResponseID)
	case msg != "":
		detail = msg
	case b.ResponseID != "":
		detail = "response_id: " + b.ResponseID
	}
	return b.ErrorCode, detail
}

// stringifyMessage flattens the oneOf<string, []string> message into a single
// line, joining array entries with "; ". Returns "" when the payload is
// neither a string nor a non-empty array of strings.
func stringifyMessage(v any) string {
	switch m := v.(type) {
	case string:
		return strings.TrimSpace(m)
	case []any:
		parts := make([]string, 0, len(m))
		for _, item := range m {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, strings.TrimSpace(s))
			}
		}
		return strings.Join(parts, "; ")
	}
	return ""
}
