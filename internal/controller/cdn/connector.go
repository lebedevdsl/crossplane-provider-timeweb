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

package cdn

import (
	"context"
	"errors"
	"fmt"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cdnv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/cdn/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

var errNotCdn = errors.New("managed resource is not a Cdn")

// connector builds a per-reconcile external client for the Cdn kind. Origin
// bucketRef resolution happens in the external (kube handle passed through) so
// the not-ready case surfaces as a condition instead of a Connect error, and
// the delete path never resolves anything. Implements managed.ExternalConnector.
type connector struct {
	kube     client.Client
	usage    resource.ModernTracker
	logger   logging.Logger
	recorder record.EventRecorder
}

// Connect resolves credentials and returns the Cdn external.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*cdnv1alpha1.Cdn)
	if !ok {
		return nil, errNotCdn
	}
	if err := c.usage.Track(ctx, cr); err != nil {
		return nil, fmt.Errorf("cdn: track ProviderConfigUsage: %w", err)
	}

	token, _, err := shared.ResolveToken(ctx, c.kube, cr.GetNamespace(), cr.GetProviderConfigReference())
	if err != nil {
		return nil, fmt.Errorf("cdn: %w", err)
	}

	tw, err := timeweb.New(timeweb.Config{
		Token:  token,
		Logger: clientLogger{l: c.logger},
	})
	if err != nil {
		return nil, fmt.Errorf("cdn: build Timeweb client: %w", err)
	}

	return &external{tw: tw, kube: c.kube, recorder: c.recorder}, nil
}

// clientLogger adapts crossplane-runtime's logging.Logger to timeweb.Logger.
type clientLogger struct{ l logging.Logger }

func (c clientLogger) Debug(msg string, kv ...any) { c.l.Debug(msg, kv...) }
func (c clientLogger) Info(msg string, kv ...any)  { c.l.Info(msg, kv...) }
