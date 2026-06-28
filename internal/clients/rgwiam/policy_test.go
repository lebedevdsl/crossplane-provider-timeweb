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

import "testing"

func mustRender(t *testing.T, grants []Grant) string {
	t.Helper()
	doc, err := RenderPolicy(grants)
	if err != nil {
		t.Fatalf("RenderPolicy: %v", err)
	}
	return doc
}

func TestRenderPolicy_Levels(t *testing.T) {
	cases := map[string]struct {
		grants []Grant
		expect string
	}{
		"empty": {
			grants: nil,
			expect: `{"Version":"2012-10-17","Statement":[
				{"Sid":"IamListAllMyBuckets","Effect":"Allow","Action":"s3:ListAllMyBuckets","Resource":["*"]}]}`,
		},
		"read": {
			grants: []Grant{{Bucket: "b", Level: LevelRead}},
			expect: `{"Version":"2012-10-17","Statement":[
				{"Effect":"Allow","Action":"s3:ListAllMyBuckets","Resource":["*"]},
				{"Effect":"Allow","Action":["s3:Get*","s3:List*"],"Resource":["arn:aws:s3:::b/*"]},
				{"Effect":"Allow","Action":["s3:Get*","s3:List*"],"Resource":["arn:aws:s3:::b"]}]}`,
		},
		"read-write": {
			grants: []Grant{{Bucket: "b", Level: LevelReadWrite}},
			expect: `{"Version":"2012-10-17","Statement":[
				{"Effect":"Allow","Action":"s3:ListAllMyBuckets","Resource":["*"]},
				{"Effect":"Allow","Action":["s3:*"],"Resource":["arn:aws:s3:::b/*"]},
				{"Effect":"Allow","Action":["s3:Get*","s3:List*"],"Resource":["arn:aws:s3:::b"]}]}`,
		},
		"admin": {
			grants: []Grant{{Bucket: "b", Level: LevelAdmin}},
			expect: `{"Version":"2012-10-17","Statement":[
				{"Effect":"Allow","Action":"s3:ListAllMyBuckets","Resource":["*"]},
				{"Effect":"Allow","Action":["s3:*"],"Resource":["arn:aws:s3:::b/*"]},
				{"Effect":"Allow","Action":["s3:*"],"Resource":["arn:aws:s3:::b"]}]}`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := mustRender(t, tc.grants)
			if !PoliciesEqual(got, tc.expect) {
				t.Errorf("rendered policy mismatch.\n got: %s", got)
			}
		})
	}
}

func TestPoliciesEqual_OrderAndSidInsensitive(t *testing.T) {
	a := mustRender(t, []Grant{{Bucket: "a", Level: LevelReadWrite}, {Bucket: "b", Level: LevelRead}})
	// Same grants, opposite input order — render sorts, so must be equal.
	b := mustRender(t, []Grant{{Bucket: "b", Level: LevelRead}, {Bucket: "a", Level: LevelReadWrite}})
	if !PoliciesEqual(a, b) {
		t.Errorf("expected order-insensitive equality")
	}
	// A different level must NOT be equal.
	c := mustRender(t, []Grant{{Bucket: "a", Level: LevelAdmin}, {Bucket: "b", Level: LevelRead}})
	if PoliciesEqual(a, c) {
		t.Errorf("expected inequality on differing level")
	}
}

func TestPolicyHash_StableAcrossOrder(t *testing.T) {
	a := mustRender(t, []Grant{{Bucket: "a", Level: LevelReadWrite}, {Bucket: "b", Level: LevelRead}})
	b := mustRender(t, []Grant{{Bucket: "b", Level: LevelRead}, {Bucket: "a", Level: LevelReadWrite}})
	if PolicyHash(a) == "" || PolicyHash(a) != PolicyHash(b) {
		t.Errorf("policy hash should be non-empty and order-stable: %q vs %q", PolicyHash(a), PolicyHash(b))
	}
}

func TestDeriveLevelForBucket(t *testing.T) {
	doc := mustRender(t, []Grant{
		{Bucket: "rw", Level: LevelReadWrite},
		{Bucket: "ro", Level: LevelRead},
		{Bucket: "adm", Level: LevelAdmin},
	})
	cases := map[string]struct {
		bucket  string
		level   string
		present bool
	}{
		"read-write": {"rw", LevelReadWrite, true},
		"read":       {"ro", LevelRead, true},
		"admin":      {"adm", LevelAdmin, true},
		"absent":     {"nope", "", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			level, present := DeriveLevelForBucket(doc, tc.bucket)
			if present != tc.present || level != tc.level {
				t.Errorf("got (%q,%v) want (%q,%v)", level, present, tc.level, tc.present)
			}
		})
	}
}
