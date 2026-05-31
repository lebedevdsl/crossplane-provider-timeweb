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

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

// Group / Version metadata for the S3Bucket managed-resource API.
const (
	Group   = "objectstorage.m.timeweb.crossplane.io"
	Version = "v1alpha1"
)

var (
	// GroupVersion is the GroupVersion exposed by this API.
	GroupVersion = schema.GroupVersion{Group: Group, Version: Version}
	// SchemeBuilder collects type registration funcs for this group.
	// SA1019: see apis/containerregistry/v1alpha1/groupversion_info.go.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion} //nolint:staticcheck // SA1019 — see CR groupversion_info
	// AddToScheme registers every kind in this package.
	AddToScheme = SchemeBuilder.AddToScheme
)

// GroupVersionKind metadata.
var (
	// S3BucketKind is the Kind name for S3Bucket.
	S3BucketKind = "S3Bucket"
	// S3BucketGroupVersionKind is the full GVK for S3Bucket.
	S3BucketGroupVersionKind = GroupVersion.WithKind(S3BucketKind)
)

func init() {
	SchemeBuilder.Register(&S3Bucket{}, &S3BucketList{})
}
