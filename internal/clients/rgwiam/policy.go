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

package rgwiam

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Access levels accepted in a Grant.
const (
	LevelRead      = "read"
	LevelReadWrite = "read-write"
	LevelAdmin     = "admin"
)

// policyVersion is the IAM policy language version the panel emits.
const policyVersion = "2012-10-17"

// Grant is one resolved (bucket, level) pair to render into the merged policy.
type Grant struct {
	Bucket string
	Level  string
}

// statement is one IAM policy statement. Action/Resource accept the scalar-or-
// array JSON the panel emits; rendering always marshals arrays.
type statement struct {
	Sid      string      `json:"Sid,omitempty"`
	Effect   string      `json:"Effect"`
	Action   flexStrings `json:"Action"`
	Resource flexStrings `json:"Resource"`
}

type policyDoc struct {
	Version   string      `json:"Version"`
	Statement []statement `json:"Statement"`
}

// flexStrings unmarshals a JSON string OR array of strings; marshals as array.
type flexStrings []string

func (f *flexStrings) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*f = flexStrings{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*f = many
	return nil
}

func (f flexStrings) MarshalJSON() ([]byte, error) { return json.Marshal([]string(f)) }

func arn(bucket string) string        { return "arn:aws:s3:::" + bucket }
func arnObjects(bucket string) string { return "arn:aws:s3:::" + bucket + "/*" }

// objectActions / bucketActions return the action set for a level.
func objectActions(level string) []string {
	if level == LevelReadWrite || level == LevelAdmin {
		return []string{"s3:*"}
	}
	return []string{"s3:Get*", "s3:List*"}
}

func bucketActions(level string) []string {
	if level == LevelAdmin {
		return []string{"s3:*"}
	}
	return []string{"s3:Get*", "s3:List*"}
}

// RenderPolicy renders the merged iam-user-policy from the grant list: the base
// account-wide list statement plus one objects+bucket statement-pair per grant.
// An empty grant list yields the base statement only. Grants are emitted sorted
// by bucket name for stable output. Sids match the panel's vocabulary.
func RenderPolicy(grants []Grant) (string, error) {
	sorted := append([]Grant(nil), grants...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Bucket < sorted[j].Bucket })

	stmts := []statement{{
		Sid:      "IamListAllMyBuckets",
		Effect:   "Allow",
		Action:   flexStrings{"s3:ListAllMyBuckets"},
		Resource: flexStrings{"*"},
	}}
	for _, g := range sorted {
		objSid := "AllowReadObjectsInBucket"
		if g.Level == LevelReadWrite || g.Level == LevelAdmin {
			objSid = "AllowFullAccessToObjects"
		}
		bktSid := "AllowReadBucketMetadata"
		if g.Level == LevelAdmin {
			bktSid = "AllowFullBucketAccess"
		}
		stmts = append(stmts,
			statement{Sid: objSid, Effect: "Allow", Action: objectActions(g.Level), Resource: flexStrings{arnObjects(g.Bucket)}},
			statement{Sid: bktSid, Effect: "Allow", Action: bucketActions(g.Level), Resource: flexStrings{arn(g.Bucket)}},
		)
	}
	out, err := json.Marshal(policyDoc{Version: policyVersion, Statement: stmts})
	if err != nil {
		return "", fmt.Errorf("rgwiam: marshal policy: %w", err)
	}
	return string(out), nil
}

// canonicalStatements reduces a policy document to a sorted set of canonical
// "effect|sortedActions|sortedResources" tuples — Sid- and order-insensitive,
// because the panel reuses Sids across buckets and does not guarantee order
// (research R-2).
func canonicalStatements(doc string) ([]string, error) {
	var p policyDoc
	if err := json.Unmarshal([]byte(doc), &p); err != nil {
		return nil, fmt.Errorf("rgwiam: parse policy: %w", err)
	}
	out := make([]string, 0, len(p.Statement))
	for _, s := range p.Statement {
		actions := append([]string(nil), s.Action...)
		resources := append([]string(nil), s.Resource...)
		sort.Strings(actions)
		sort.Strings(resources)
		out = append(out, strings.ToLower(s.Effect)+"|"+strings.Join(actions, ",")+"|"+strings.Join(resources, ","))
	}
	sort.Strings(out)
	return out, nil
}

// PoliciesEqual reports whether two policy documents grant the same thing,
// ignoring Sid and statement order. A parse failure on either side ⇒ not equal.
func PoliciesEqual(a, b string) bool {
	ca, err := canonicalStatements(a)
	if err != nil {
		return false
	}
	cb, err := canonicalStatements(b)
	if err != nil {
		return false
	}
	if len(ca) != len(cb) {
		return false
	}
	for i := range ca {
		if ca[i] != cb[i] {
			return false
		}
	}
	return true
}

// PolicyHash returns a stable hash of a policy document's semantic content
// (order/Sid-insensitive). Empty string on parse failure.
func PolicyHash(doc string) string {
	c, err := canonicalStatements(doc)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(c, "\n")))
	return hex.EncodeToString(sum[:])
}

// DeriveLevelForBucket inspects a policy document and returns the access level
// granted to bucket (and whether any grant is present) — used to build the
// S3Bucket.status.attachedUsers mirror.
func DeriveLevelForBucket(doc, bucket string) (level string, present bool) {
	var p policyDoc
	if err := json.Unmarshal([]byte(doc), &p); err != nil {
		return "", false
	}
	objArn := arnObjects(bucket)
	bktArn := arn(bucket)
	objFull, bktFull := false, false
	for _, s := range p.Statement {
		full := contains(s.Action, "s3:*")
		if contains(s.Resource, objArn) {
			present = true
			objFull = objFull || full
		}
		if contains(s.Resource, bktArn) {
			present = true
			bktFull = bktFull || full
		}
	}
	if !present {
		return "", false
	}
	switch {
	case objFull && bktFull:
		return LevelAdmin, true
	case objFull:
		return LevelReadWrite, true
	default:
		return LevelRead, true
	}
}

func contains(xs flexStrings, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
