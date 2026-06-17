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

package s3bucket

import (
	"context"
	"fmt"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	objectstoragev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/objectstorage/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	twgen "github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

type connector struct {
	kube     client.Client
	usage    resource.ModernTracker
	logger   logging.Logger
	recorder record.EventRecorder
	// cache is the Setup-scoped resolver cache shared across reconciles —
	// a per-Connect cache never gets a hit (feature-006 foundational fix).
	cache *resolver.Cache
}

// Connect implements managed.ExternalConnector.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*objectstoragev1alpha1.S3Bucket)
	if !ok {
		return nil, errNotS3Bucket
	}
	if err := c.usage.Track(ctx, cr); err != nil {
		return nil, fmt.Errorf("s3bucket: track ProviderConfigUsage: %w", err)
	}

	token, _, err := shared.ResolveToken(ctx, c.kube, cr.GetNamespace(), cr.GetProviderConfigReference())
	if err != nil {
		return nil, fmt.Errorf("s3bucket: %w", err)
	}

	tw, err := timeweb.New(timeweb.Config{
		Token:  token,
		Logger: clientLogger{l: c.logger},
	})
	if err != nil {
		return nil, fmt.Errorf("s3bucket: build Timeweb client: %w", err)
	}
	return &external{
		tw:       tw.ClientInterface,
		recorder: c.recorder,
		resolver: resolver.New(&twgen.ClientWithResponses{ClientInterface: tw.ClientInterface}, resolver.Options{SharedCache: c.cache}),
		pcRef: resolver.PCRef{
			Kind:      cr.GetProviderConfigReference().Kind,
			Name:      cr.GetProviderConfigReference().Name,
			Namespace: cr.GetNamespace(),
		},
	}, nil
}

type clientLogger struct{ l logging.Logger }

func (c clientLogger) Debug(msg string, kv ...any) { c.l.Debug(msg, kv...) }
func (c clientLogger) Info(msg string, kv ...any)  { c.l.Info(msg, kv...) }
