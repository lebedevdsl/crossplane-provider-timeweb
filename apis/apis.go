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

// Package apis is the parent of every per-group CRD type package in the
// provider. cmd/provider/main.go calls AddToScheme to register every kind
// at startup; per-group registration is delegated to the group's own
// SchemeBuilder.
package apis

import (
	"k8s.io/apimachinery/pkg/runtime"

	computev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/compute/v1alpha1"
	containerregistryv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/containerregistry/v1alpha1"
	networkv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/network/v1alpha1"
	objectstoragev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/objectstorage/v1alpha1"
	projectv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/project/v1alpha1"
	sshkeyv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/sshkey/v1alpha1"
	apisv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/v1alpha1"
)

// AddToSchemes aggregates per-group AddToScheme functions. Append to this
// slice when a new managed-resource group is introduced.
var AddToSchemes = runtime.SchemeBuilder{
	apisv1alpha1.AddToScheme,
	projectv1alpha1.AddToScheme,
	sshkeyv1alpha1.AddToScheme,
	objectstoragev1alpha1.AddToScheme,
	containerregistryv1alpha1.AddToScheme,
	computev1alpha1.AddToScheme,
	networkv1alpha1.AddToScheme,
}

// AddToScheme registers every provider kind with s.
func AddToScheme(s *runtime.Scheme) error {
	return AddToSchemes.AddToScheme(s)
}
