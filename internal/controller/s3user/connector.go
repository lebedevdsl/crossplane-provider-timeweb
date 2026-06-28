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

package s3user

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	objectstoragev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/objectstorage/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/rgwiam"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

// Sentinel errors for grant resolution. Returned from Connect (the Router-style
// gate idiom): the runtime surfaces Synced=False with the message.
var (
	errNotS3User       = errors.New("managed resource is not a S3User")
	errTargetNotFound  = errors.New("s3user: referenced S3Bucket not found in same namespace")
	errTargetNotReady  = errors.New("s3user: referenced S3Bucket not yet ready")
	errDuplicateBucket = errors.New("s3user: duplicate bucket in bucketAccess")
	errGrantSpec       = errors.New("s3user: each bucketAccess entry needs exactly one of bucketRef/bucketName")
)

type connector struct {
	kube     client.Client
	usage    resource.ModernTracker
	logger   logging.Logger
	recorder record.EventRecorder
}

// Connect builds the per-reconcile external client: it resolves the token,
// derives the account super-user's S3 keys at runtime (never cached — FR-011),
// and resolves the bucketAccess grants to upstream names.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*objectstoragev1alpha1.S3User)
	if !ok {
		return nil, errNotS3User
	}
	if err := c.usage.Track(ctx, cr); err != nil {
		return nil, fmt.Errorf("s3user: track ProviderConfigUsage: %w", err)
	}

	token, _, err := shared.ResolveToken(ctx, c.kube, cr.GetNamespace(), cr.GetProviderConfigReference())
	if err != nil {
		return nil, fmt.Errorf("s3user: %w", err)
	}

	tw, err := timeweb.New(timeweb.Config{Token: token, Logger: clientLogger{l: c.logger}})
	if err != nil {
		return nil, fmt.Errorf("s3user: build Timeweb client: %w", err)
	}

	ak, sk, err := deriveAdminKeys(ctx, tw)
	if err != nil {
		return nil, err
	}

	grants, err := c.resolveGrants(ctx, cr)
	if err != nil {
		return nil, err
	}

	return &external{
		tw:       tw,
		iam:      rgwiam.New(rgwiam.Config{AccessKeyID: ak, SecretAccessKey: sk}),
		recorder: c.recorder,
		grants:   grants,
	}, nil
}

// deriveAdminKeys reads the account super-user's S3 keys from the v1 endpoint.
// Re-read every reconcile so an out-of-band key reset is picked up; never
// cached across reconciles.
func deriveAdminKeys(ctx context.Context, tw *timeweb.Client) (string, string, error) {
	resp, err := tw.GetStorageUsers(ctx)
	if err != nil {
		return "", "", timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return "", "", err
	}
	var env struct {
		Users []struct {
			AccessKey string `json:"access_key"`
			SecretKey string `json:"secret_key"`
		} `json:"users"`
	}
	if err := timeweb.DecodeBody(resp.Body, &env); err != nil {
		return "", "", err
	}
	if len(env.Users) == 0 || env.Users[0].AccessKey == "" || env.Users[0].SecretKey == "" {
		return "", "", fmt.Errorf("s3user: no account-admin S3 keys found at /api/v1/storages/users")
	}
	return env.Users[0].AccessKey, env.Users[0].SecretKey, nil
}

// resolveGrants resolves every bucketAccess entry to (bucketName, level),
// rejecting duplicates. bucketRef targets must exist and be Ready=True.
//
// During deletion the grants aren't needed (Delete removes the user + its
// constant-named policy by external-name), and a referenced bucket may already
// be gone or mid-delete. Hard-failing here would block Connect — and therefore
// the final reconcile that removes the finalizer — wedging the S3User forever.
// So when the resource is being deleted, skip refs that don't resolve.
func (c *connector) resolveGrants(ctx context.Context, cr *objectstoragev1alpha1.S3User) ([]rgwiam.Grant, error) {
	deleting := cr.GetDeletionTimestamp() != nil
	out := make([]rgwiam.Grant, 0, len(cr.Spec.ForProvider.BucketAccess))
	seen := map[string]struct{}{}
	for i := range cr.Spec.ForProvider.BucketAccess {
		g := cr.Spec.ForProvider.BucketAccess[i]
		name, err := c.resolveBucketName(ctx, cr.GetNamespace(), g)
		if err != nil {
			if deleting {
				continue
			}
			return nil, err
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("%w: %q", errDuplicateBucket, name)
		}
		seen[name] = struct{}{}
		out = append(out, rgwiam.Grant{Bucket: name, Level: g.AccessLevel})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bucket < out[j].Bucket })
	return out, nil
}

// resolveBucketName resolves one grant to a bucket name (by ref or direct name).
func (c *connector) resolveBucketName(ctx context.Context, ns string, g objectstoragev1alpha1.BucketGrant) (string, error) {
	switch {
	case g.BucketName != nil && *g.BucketName != "" && g.BucketRef == nil:
		return *g.BucketName, nil
	case g.BucketRef != nil && (g.BucketName == nil || *g.BucketName == ""):
		target := &objectstoragev1alpha1.S3Bucket{}
		if err := c.kube.Get(ctx, types.NamespacedName{Namespace: ns, Name: g.BucketRef.Name}, target); err != nil {
			if kerrors.IsNotFound(err) {
				return "", fmt.Errorf("%w: S3Bucket %q", errTargetNotFound, g.BucketRef.Name)
			}
			return "", fmt.Errorf("get S3Bucket %s/%s: %w", ns, g.BucketRef.Name, err)
		}
		if target.GetCondition(xpv2.TypeReady).Status != corev1.ConditionTrue {
			return "", fmt.Errorf("%w: S3Bucket %q (not Ready=True)", errTargetNotReady, g.BucketRef.Name)
		}
		if target.Spec.ForProvider.Name == "" {
			return "", fmt.Errorf("%w: S3Bucket %q (empty bucket name)", errTargetNotReady, g.BucketRef.Name)
		}
		return target.Spec.ForProvider.Name, nil
	default:
		return "", fmt.Errorf("%w", errGrantSpec)
	}
}

type clientLogger struct{ l logging.Logger }

func (c clientLogger) Debug(msg string, kv ...any) { c.l.Debug(msg, kv...) }
func (c clientLogger) Info(msg string, kv ...any)  { c.l.Info(msg, kv...) }
