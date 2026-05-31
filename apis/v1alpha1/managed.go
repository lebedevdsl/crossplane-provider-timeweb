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
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
)

// Forwarders required by crossplane-runtime's `resource.ProviderConfig`
// (Conditioned + UserCounter) and `resource.TypedProviderConfigUsage`
// (Object + RequiredTypedResourceReferencer with typed PC ref)
// interfaces. The embedded structs don't promote pointer-receiver
// methods to the outer type, so every PC and PCU kind needs its own
// copy.

// --- ProviderConfig (namespaced) -------------------------------------------

func (pc *ProviderConfig) GetCondition(ct xpv2.ConditionType) xpv2.Condition {
	return pc.Status.GetCondition(ct)
}
func (pc *ProviderConfig) SetConditions(c ...xpv2.Condition) { pc.Status.SetConditions(c...) }
func (pc *ProviderConfig) GetUsers() int64                   { return pc.Status.Users }
func (pc *ProviderConfig) SetUsers(n int64)                  { pc.Status.Users = n }

// --- ClusterProviderConfig (cluster-scoped) -------------------------------

func (pc *ClusterProviderConfig) GetCondition(ct xpv2.ConditionType) xpv2.Condition {
	return pc.Status.GetCondition(ct)
}
func (pc *ClusterProviderConfig) SetConditions(c ...xpv2.Condition) { pc.Status.SetConditions(c...) }
func (pc *ClusterProviderConfig) GetUsers() int64                   { return pc.Status.Users }
func (pc *ClusterProviderConfig) SetUsers(n int64)                  { pc.Status.Users = n }

// --- ProviderConfigUsage (namespaced) --------------------------------------

func (pcu *ProviderConfigUsage) GetProviderConfigReference() xpv2.ProviderConfigReference {
	return pcu.ProviderConfigReference
}
func (pcu *ProviderConfigUsage) SetProviderConfigReference(r xpv2.ProviderConfigReference) {
	pcu.ProviderConfigReference = r
}
func (pcu *ProviderConfigUsage) GetResourceReference() xpv2.TypedReference {
	return pcu.ResourceReference
}
func (pcu *ProviderConfigUsage) SetResourceReference(r xpv2.TypedReference) {
	pcu.ResourceReference = r
}

// GetItems implements resource.ProviderConfigUsageList — required when the
// list type is passed to crossplane-runtime's usage tracker.
func (l *ProviderConfigUsageList) GetItems() []resource.ProviderConfigUsage {
	items := make([]resource.ProviderConfigUsage, len(l.Items))
	for i := range l.Items {
		items[i] = &l.Items[i]
	}
	return items
}

// --- ClusterProviderConfigUsage (cluster-scoped) ---------------------------

func (pcu *ClusterProviderConfigUsage) GetProviderConfigReference() xpv2.ProviderConfigReference {
	return pcu.ProviderConfigReference
}
func (pcu *ClusterProviderConfigUsage) SetProviderConfigReference(r xpv2.ProviderConfigReference) {
	pcu.ProviderConfigReference = r
}
func (pcu *ClusterProviderConfigUsage) GetResourceReference() xpv2.TypedReference {
	return pcu.ResourceReference
}
func (pcu *ClusterProviderConfigUsage) SetResourceReference(r xpv2.TypedReference) {
	pcu.ResourceReference = r
}

func (l *ClusterProviderConfigUsageList) GetItems() []resource.ProviderConfigUsage {
	items := make([]resource.ProviderConfigUsage, len(l.Items))
	for i := range l.Items {
		items[i] = &l.Items[i]
	}
	return items
}

// --- Credentials abstraction -----------------------------------------------
//
// The namespaced ProviderConfig and the cluster-scoped ClusterProviderConfig
// have different SecretRef schemas (LocalSecretKeySelector vs
// SecretKeySelector), so we expose a small flat interface that hides the
// shape and lets the connector use a uniform call sequence.

// PCKind names a ProviderConfig kind. Used as the value of
// `spec.providerConfigRef.kind` on managed resources and as a
// discriminator in connector dual-lookup helpers.
const (
	PCKindNamespaced = "ProviderConfig"
	PCKindCluster    = "ClusterProviderConfig"
)

// CredentialedProviderConfig is the read-only surface a connector needs
// to fetch the API token. Both ProviderConfig and ClusterProviderConfig
// satisfy it via their separate concrete credential types.
// +kubebuilder:object:generate=false
type CredentialedProviderConfig interface {
	// GetCredentialsSource returns the credential source enum
	// (only "Secret" supported in v0.1).
	GetCredentialsSource() xpv2.CredentialsSource
	// GetCredentialsSecretName returns the Secret name. Empty if the
	// operator did not set spec.credentials.secretRef.
	GetCredentialsSecretName() string
	// GetCredentialsSecretKey returns the key within the Secret that
	// holds the API token. Empty if not set.
	GetCredentialsSecretKey() string
	// GetCredentialsSecretNamespace returns the Secret namespace:
	//   - For ProviderConfig (namespaced), this is the PC's own namespace
	//     (CEL forbids setting it on the namespaced kind's secretRef).
	//   - For ClusterProviderConfig, this is the explicit
	//     spec.credentials.secretRef.namespace.
	GetCredentialsSecretNamespace() string
}

// --- ProviderConfig (namespaced) accessors --------------------------------

func (pc *ProviderConfig) GetCredentialsSource() xpv2.CredentialsSource {
	return pc.Spec.Credentials.Source
}
func (pc *ProviderConfig) GetCredentialsSecretName() string {
	if pc.Spec.Credentials.SecretRef == nil {
		return ""
	}
	return pc.Spec.Credentials.SecretRef.Name
}
func (pc *ProviderConfig) GetCredentialsSecretKey() string {
	if pc.Spec.Credentials.SecretRef == nil {
		return ""
	}
	return pc.Spec.Credentials.SecretRef.Key
}
func (pc *ProviderConfig) GetCredentialsSecretNamespace() string { return pc.GetNamespace() }

// --- ClusterProviderConfig accessors --------------------------------------

func (pc *ClusterProviderConfig) GetCredentialsSource() xpv2.CredentialsSource {
	return pc.Spec.Credentials.Source
}
func (pc *ClusterProviderConfig) GetCredentialsSecretName() string {
	if pc.Spec.Credentials.SecretRef == nil {
		return ""
	}
	return pc.Spec.Credentials.SecretRef.Name
}
func (pc *ClusterProviderConfig) GetCredentialsSecretKey() string {
	if pc.Spec.Credentials.SecretRef == nil {
		return ""
	}
	return pc.Spec.Credentials.SecretRef.Key
}
func (pc *ClusterProviderConfig) GetCredentialsSecretNamespace() string {
	if pc.Spec.Credentials.SecretRef == nil {
		return ""
	}
	return pc.Spec.Credentials.SecretRef.Namespace
}
