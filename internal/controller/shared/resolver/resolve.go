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

package resolver

import (
	"context"
)

// New returns a Resolver wired to the supplied CatalogClient and the
// default dimension registry. The CatalogClient is typically the
// `*generated.ClientWithResponses` value built per ProviderConfig at MR
// connect time — but the interface decoupling lets tests substitute a
// fake.
func New(client CatalogClient, opts Options) Resolver {
	return &resolverImpl{
		client:   client,
		cache:    newCache(opts),
		registry: defaultRegistry(),
	}
}

// resolverImpl is the production Resolver. Stateless apart from the
// cache; safe for concurrent use.
type resolverImpl struct {
	client   CatalogClient
	cache    *cache
	registry map[string]dimensionDef
}

func (r *resolverImpl) Resolve(ctx context.Context, pcRef PCRef, dim Dimension, input ResolveInput) (ResolveOutput, error) {
	def, ok := r.registry[dim.Name]
	if !ok {
		return nil, &dimensionNotRegisteredError{Name: dim.Name}
	}
	if def.kind != dim.Kind {
		return nil, ErrInvalidInput
	}

	key := cacheKey{pc: pcRef, dim: dim}
	payload, err := r.cache.getOrFetch(ctx, key, func(ctx context.Context) (any, error) {
		return def.fetch(ctx, r.client)
	})
	if err != nil {
		return nil, err
	}

	switch dim.Kind {
	case DimensionPreset:
		entries, ok := payload.([]PresetEntry)
		if !ok {
			return nil, ErrInvalidInput
		}
		switch in := input.(type) {
		case PresetInput:
			id, err := MatchPresetSlug(in.Slug, entries, dim.Name)
			if err != nil {
				return nil, err
			}
			return PresetOutput{UpstreamID: id}, nil
		case PresetBySizeInput:
			id, err := MatchPresetBySize(in, entries, dim.Name)
			if err != nil {
				return nil, err
			}
			return PresetOutput{UpstreamID: id}, nil
		default:
			return nil, ErrInvalidInput
		}

	case DimensionConfigurator:
		in, ok := input.(ConfiguratorInput)
		if !ok {
			return nil, ErrInvalidInput
		}
		entries, ok := payload.([]ConfiguratorEntry)
		if !ok {
			return nil, ErrInvalidInput
		}
		return SelectConfigurator(in, entries, dim.Name)

	case DimensionEnum:
		in, ok := input.(EnumInput)
		if !ok {
			return nil, ErrInvalidInput
		}
		values, ok := payload.([]string)
		if !ok {
			return nil, ErrInvalidInput
		}
		for _, v := range values {
			if v == in.Value {
				return EnumOutput{Valid: true}, nil
			}
		}
		return EnumOutput{Valid: false, ValidValues: values}, &DimensionValueNotFoundError{
			Value: in.Value, ValidValues: values, DimensionID: dim.Name,
		}

	default:
		return nil, ErrInvalidInput
	}
}

func (r *resolverImpl) Invalidate(pcRef PCRef, dim Dimension) {
	r.cache.invalidate(cacheKey{pc: pcRef, dim: dim})
}

// dimensionNotRegisteredError wraps ErrUnknownDimension so callers can
// distinguish "dimension typo" from runtime fetch failures.
type dimensionNotRegisteredError struct{ Name string }

func (e *dimensionNotRegisteredError) Error() string {
	return ErrUnknownDimension.Error() + ": " + e.Name
}
func (e *dimensionNotRegisteredError) Unwrap() error { return ErrUnknownDimension }
