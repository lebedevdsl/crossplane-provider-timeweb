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

	t.Run("NotFound", func(t *testing.T) {
		err := Classify(newResp(http.StatusNotFound, ""))
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("got %v, want errors.Is(_, ErrNotFound) = true", err)
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
