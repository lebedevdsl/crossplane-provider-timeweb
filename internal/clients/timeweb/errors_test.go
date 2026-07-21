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
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func newResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestClassify(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		for _, status := range []int{200, 201, 204} {
			if err := Classify(newResp(status, "{}")); err != nil {
				t.Errorf("status %d: got %v, want nil", status, err)
			}
		}
	})

	// A 404 is "deleted" ONLY when it carries the canonical Timeweb error
	// envelope (error_code present). A bare/edge 404 (empty, HTML, or JSON
	// without error_code) is a suspected upstream flap → transient, NEVER
	// not-found — the postmortem-#124 fix that stops recreating live resources.
	t.Run("NotFound_canonicalEnvelope", func(t *testing.T) {
		cases := []string{
			// C1: full envelope
			`{"status_code":404,"error_code":"not_found","message":"Resource not found","response_id":"abc"}`,
			// C5: minimal envelope (error_code only)
			`{"error_code":"not_found"}`,
		}
		for _, body := range cases {
			err := Classify(newResp(http.StatusNotFound, body))
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("body %q: got %v, want errors.Is(_, ErrNotFound) = true", body, err)
			}
			if errors.Is(err, ErrTransient) {
				t.Errorf("body %q: enveloped 404 must not be transient, got %v", body, err)
			}
		}
	})

	t.Run("NotFound_canonicalEnvelope_surfacesDetail", func(t *testing.T) {
		// C1: message + response_id enrich the surfaced error (FR-003).
		body := `{"status_code":404,"error_code":"not_found","message":"VPC not found","response_id":"req-42"}`
		err := Classify(newResp(http.StatusNotFound, body))
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("got %v, want ErrNotFound", err)
		}
		for _, want := range []string{"VPC not found", "req-42"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q missing %q", err.Error(), want)
			}
		}
	})

	t.Run("NotFound_bareOrEdge_isTransient", func(t *testing.T) {
		// C2 empty, C3 HTML edge page, C4 JSON without error_code — all lack the
		// canonical envelope → transient (requeue), never treated as deleted.
		cases := map[string]string{
			"empty":           "",
			"html_edge":       "<html><body>404 Not Found</body></html>",
			"json_no_code":    `{"foo":"bar"}`,
			"json_empty_code": `{"error_code":"","message":"x"}`,
		}
		for name, body := range cases {
			err := Classify(newResp(http.StatusNotFound, body))
			if errors.Is(err, ErrNotFound) {
				t.Errorf("%s: bare/edge 404 must NOT be ErrNotFound, got %v", name, err)
			}
			if !errors.Is(err, ErrTransient) {
				t.Errorf("%s: want errors.Is(_, ErrTransient) = true, got %v", name, err)
			}
		}
	})

	t.Run("NotFound_transientReason_isDescriptive", func(t *testing.T) {
		// FR-007: the reclassified-404 transient must be observable via reason.
		err := Classify(newResp(http.StatusNotFound, ""))
		if !strings.Contains(err.Error(), "canonical error envelope") {
			t.Errorf("transient reason %q should name the cause (canonical error envelope)", err.Error())
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		cases := []int{
			http.StatusRequestTimeout,      // 408
			http.StatusConflict,            // 409
			http.StatusTooEarly,            // 425
			http.StatusTooManyRequests,     // 429
			http.StatusInternalServerError, // 500
			http.StatusBadGateway,          // 502
			http.StatusServiceUnavailable,  // 503
			http.StatusGatewayTimeout,      // 504
		}
		for _, status := range cases {
			err := Classify(newResp(status, ""))
			if !errors.Is(err, ErrTransient) {
				t.Errorf("status %d: got %v, want errors.Is(_, ErrTransient) = true", status, err)
			}
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		body := `{"status_code":400,"error_code":"bad_request","message":"Value must be a number"}`
		err := Classify(newResp(http.StatusBadRequest, body))
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("got %v, want *APIError", err)
		}
		if apiErr.StatusCode != http.StatusBadRequest {
			t.Errorf("StatusCode = %d, want 400", apiErr.StatusCode)
		}
		if apiErr.Code != "bad_request" {
			t.Errorf("Code = %q, want %q", apiErr.Code, "bad_request")
		}
		if !strings.Contains(apiErr.Message, "Value must be a number") {
			t.Errorf("Message = %q, want it to contain the upstream message", apiErr.Message)
		}
	})

	t.Run("NilResponse_IsTransient", func(t *testing.T) {
		err := Classify(nil)
		if !errors.Is(err, ErrTransient) {
			t.Errorf("got %v, want transient on nil response", err)
		}
	})

	t.Run("ArrayMessage_FlattensJoined", func(t *testing.T) {
		body := `{"status_code":400,"error_code":"validation","message":["a","b","c"]}`
		err := Classify(newResp(http.StatusBadRequest, body))
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("got %v, want *APIError", err)
		}
		if apiErr.Message != "a; b; c" {
			t.Errorf("Message = %q, want %q", apiErr.Message, "a; b; c")
		}
	})
}

func TestClassifyNetworkError(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		if err := ClassifyNetworkError(nil); err != nil {
			t.Errorf("nil input: got %v, want nil", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		boom := errors.New("dns lookup failed")
		err := ClassifyNetworkError(boom)
		if !errors.Is(err, ErrTransient) {
			t.Errorf("got %v, want transient", err)
		}
		if !errors.Is(err, boom) {
			t.Errorf("Underlying not preserved: %v", err)
		}
	})
}

func TestAPIErrorString(t *testing.T) {
	t.Run("WithCode", func(t *testing.T) {
		e := &APIError{StatusCode: 400, Code: "bad_request", Message: "oops"}
		want := "timeweb api: 400 bad_request: oops"
		if got := e.Error(); got != want {
			t.Errorf("Error() = %q, want %q", got, want)
		}
	})

	t.Run("WithoutCode", func(t *testing.T) {
		e := &APIError{StatusCode: 500, Message: "Internal Server Error"}
		want := "timeweb api: 500: Internal Server Error"
		if got := e.Error(); got != want {
			t.Errorf("Error() = %q, want %q", got, want)
		}
	})
}
