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
	"fmt"
	"io"
)

// DecodeBody reads at most 1 MiB of r and unmarshals it into v.
// Every external client MUST use it instead of hand-rolled
// io.ReadAll+json.Unmarshal: ignored decode errors have produced real
// failure modes (a nodepool decode failure made Update compute a scale
// delta against a zero observation — feature 006 review finding B3).
func DecodeBody(r io.Reader, v any) error {
	b, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("decode body: %w", err)
	}
	return nil
}
