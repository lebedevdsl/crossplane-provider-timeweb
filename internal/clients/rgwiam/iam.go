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

// Package rgwiam is the narrow client for Timeweb's Ceph-RGW IAM Query surface
// (the AWS-IAM-compatible admin API exposed at https://panel.s3.twcstorage.ru/).
// It is the ONLY package in this provider that imports the AWS SDK: the s3user
// controller depends solely on the Client interface here, signs requests with
// the account super-user's S3 keys, and converges every user to a single merged
// inline policy named PolicyName (the convention the Timeweb panel reads/writes).
//
// All policy rendering and the drift comparison are pure helpers in policy.go;
// the SigV4 signing + IAM Query transport live in sigv4.go.
package rgwiam

import (
	"context"
	"errors"
)

// PolicyName is the single inline policy that holds ALL of a user's bucket
// grants. The Timeweb panel reads/writes exactly this name; the controller
// matches it so panel and provider edits do not produce competing policies
// (research R-1). RGW supports multiple inline policies per user, but we use
// only this one.
const PolicyName = "iam-user-policy"

// DefaultEndpoint / DefaultRegion / iamService are the verified IAM-signing
// parameters (research R-7): SigV4 service "iam", region "ru-1", panel host.
const (
	DefaultEndpoint = "https://panel.s3.twcstorage.ru/"
	DefaultRegion   = "ru-1"
	iamService      = "iam"
)

// ErrNoSuchEntity signals that a user policy does not exist (RGW IAM
// `NoSuchEntity`). On Observe it means the policy is not yet attached (drift);
// on Delete it means already-gone (success).
var ErrNoSuchEntity = errors.New("rgwiam: no such entity")

// ErrTransient marks a retryable failure (5xx / 429 / transport / timeout).
// The controller requeues without flapping conditions.
var ErrTransient = errors.New("rgwiam: transient error")

// QueryError is a terminal IAM Query API error (a 4xx other than NoSuchEntity,
// e.g. MalformedPolicyDocument). It reports as transient via errors.Is when the
// HTTP status is retryable.
type QueryError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *QueryError) Error() string {
	return "rgwiam: IAM query error: code=" + e.Code + " status=" + itoa(e.StatusCode) + " msg=" + e.Message
}

// Is lets errors.Is(err, ErrTransient) succeed for retryable HTTP statuses.
func (e *QueryError) Is(target error) bool {
	if target == ErrTransient {
		return e.StatusCode == 429 || e.StatusCode == 0 || (e.StatusCode >= 500 && e.StatusCode <= 599)
	}
	return false
}

// Client is the IAM-grant surface the s3user controller depends on. All grants
// for a user live in the single PolicyName document.
//
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -generate
//counterfeiter:generate -o rgwiamfakes/fake_client.go -fake-name FakeClient . Client
type Client interface {
	// PutUserPolicy attaches/replaces the inline policy as a whole.
	PutUserPolicy(ctx context.Context, userName, policyName, policyDocument string) error
	// GetUserPolicy returns the inline policy document (JSON). Returns
	// ErrNoSuchEntity if the policy is not attached.
	GetUserPolicy(ctx context.Context, userName, policyName string) (string, error)
	// ListUserPolicies returns the inline policy names for the user.
	ListUserPolicies(ctx context.Context, userName string) ([]string, error)
	// DeleteUserPolicy removes one inline policy. ErrNoSuchEntity ⇒ already gone.
	DeleteUserPolicy(ctx context.Context, userName, policyName string) error
}

// itoa avoids importing strconv just for error strings.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
