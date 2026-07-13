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

import "testing"

// The rate budget must be process-global per host, so N clients (i.e. N
// reconciles) draw from ONE limiter — not N independent 2 r/s budgets.
func TestSharedLimiterIsPerHost(t *testing.T) {
	a := sharedLimiter("https://api.timeweb.cloud", DefaultRateLimit, DefaultBurst)
	b := sharedLimiter("https://api.timeweb.cloud/api/v1/x", DefaultRateLimit, DefaultBurst)
	if a != b {
		t.Fatal("same host must share ONE limiter (per-reconcile multiplication bug)")
	}
	ta := sharedTransport("https://api.timeweb.cloud")
	tb := sharedTransport("https://api.timeweb.cloud")
	if ta != tb {
		t.Fatal("same host must share ONE base transport (connection reuse)")
	}
}

// Two clients built with different tokens must still share the budget but keep
// isolated auth (no token bleed).
func TestClientsShareLimiterIsolatedAuth(t *testing.T) {
	c1, err := New(Config{Token: "tok-1"})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := New(Config{Token: "tok-2"})
	if err != nil {
		t.Fatal(err)
	}
	if c1.limiter != c2.limiter {
		t.Fatal("clients on the same host must share the process-global limiter")
	}
	at1 := c1.httpDoer.Transport.(*authTransport)
	at2 := c2.httpDoer.Transport.(*authTransport)
	if at1.token == at2.token {
		t.Fatal("per-client bearer token must stay isolated")
	}
	if at1.next != at2.next {
		t.Fatal("auth transports should wrap the SAME shared base transport")
	}
}
