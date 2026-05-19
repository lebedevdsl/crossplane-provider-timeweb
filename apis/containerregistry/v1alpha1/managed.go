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

import xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"

// --- ContainerRegistry --------------------------------------------------------

// GetCondition returns the matching condition.
func (mg *ContainerRegistry) GetCondition(ct xpv1.ConditionType) xpv1.Condition {
	return mg.Status.GetCondition(ct)
}

// GetDeletionPolicy returns the DeletionPolicy.
func (mg *ContainerRegistry) GetDeletionPolicy() xpv1.DeletionPolicy {
	return mg.Spec.DeletionPolicy
}

// GetProviderConfigReference returns the ProviderConfig reference.
func (mg *ContainerRegistry) GetProviderConfigReference() *xpv1.Reference {
	return mg.Spec.ProviderConfigReference
}

// GetPublishConnectionDetailsTo returns the connection-details destination.
func (mg *ContainerRegistry) GetPublishConnectionDetailsTo() *xpv1.PublishConnectionDetailsTo {
	return mg.Spec.PublishConnectionDetailsTo
}

// GetManagementPolicies returns the management policies.
func (mg *ContainerRegistry) GetManagementPolicies() xpv1.ManagementPolicies {
	return mg.Spec.ManagementPolicies
}

// GetWriteConnectionSecretToReference returns the connection-Secret target.
func (mg *ContainerRegistry) GetWriteConnectionSecretToReference() *xpv1.SecretReference {
	return mg.Spec.WriteConnectionSecretToReference
}

// SetConditions sets one or more conditions on status.
func (mg *ContainerRegistry) SetConditions(c ...xpv1.Condition) { mg.Status.SetConditions(c...) }

// SetDeletionPolicy sets the DeletionPolicy.
func (mg *ContainerRegistry) SetDeletionPolicy(r xpv1.DeletionPolicy) {
	mg.Spec.DeletionPolicy = r
}

// SetProviderConfigReference sets the ProviderConfig reference.
func (mg *ContainerRegistry) SetProviderConfigReference(r *xpv1.Reference) {
	mg.Spec.ProviderConfigReference = r
}

// SetPublishConnectionDetailsTo sets the connection-details destination.
func (mg *ContainerRegistry) SetPublishConnectionDetailsTo(r *xpv1.PublishConnectionDetailsTo) {
	mg.Spec.PublishConnectionDetailsTo = r
}

// SetManagementPolicies sets the management policies.
func (mg *ContainerRegistry) SetManagementPolicies(r xpv1.ManagementPolicies) {
	mg.Spec.ManagementPolicies = r
}

// SetWriteConnectionSecretToReference sets the connection-Secret target.
func (mg *ContainerRegistry) SetWriteConnectionSecretToReference(r *xpv1.SecretReference) {
	mg.Spec.WriteConnectionSecretToReference = r
}

// --- ContainerRegistryRepository ---------------------------------------------

// GetCondition returns the matching condition.
func (mg *ContainerRegistryRepository) GetCondition(ct xpv1.ConditionType) xpv1.Condition {
	return mg.Status.GetCondition(ct)
}

// GetDeletionPolicy returns the DeletionPolicy.
func (mg *ContainerRegistryRepository) GetDeletionPolicy() xpv1.DeletionPolicy {
	return mg.Spec.DeletionPolicy
}

// GetProviderConfigReference returns the ProviderConfig reference.
func (mg *ContainerRegistryRepository) GetProviderConfigReference() *xpv1.Reference {
	return mg.Spec.ProviderConfigReference
}

// GetPublishConnectionDetailsTo returns the connection-details destination.
func (mg *ContainerRegistryRepository) GetPublishConnectionDetailsTo() *xpv1.PublishConnectionDetailsTo {
	return mg.Spec.PublishConnectionDetailsTo
}

// GetManagementPolicies returns the management policies.
func (mg *ContainerRegistryRepository) GetManagementPolicies() xpv1.ManagementPolicies {
	return mg.Spec.ManagementPolicies
}

// GetWriteConnectionSecretToReference returns the connection-Secret target.
func (mg *ContainerRegistryRepository) GetWriteConnectionSecretToReference() *xpv1.SecretReference {
	return mg.Spec.WriteConnectionSecretToReference
}

// SetConditions sets one or more conditions.
func (mg *ContainerRegistryRepository) SetConditions(c ...xpv1.Condition) {
	mg.Status.SetConditions(c...)
}

// SetDeletionPolicy sets the DeletionPolicy.
func (mg *ContainerRegistryRepository) SetDeletionPolicy(r xpv1.DeletionPolicy) {
	mg.Spec.DeletionPolicy = r
}

// SetProviderConfigReference sets the ProviderConfig reference.
func (mg *ContainerRegistryRepository) SetProviderConfigReference(r *xpv1.Reference) {
	mg.Spec.ProviderConfigReference = r
}

// SetPublishConnectionDetailsTo sets the connection-details destination.
func (mg *ContainerRegistryRepository) SetPublishConnectionDetailsTo(r *xpv1.PublishConnectionDetailsTo) {
	mg.Spec.PublishConnectionDetailsTo = r
}

// SetManagementPolicies sets the management policies.
func (mg *ContainerRegistryRepository) SetManagementPolicies(r xpv1.ManagementPolicies) {
	mg.Spec.ManagementPolicies = r
}

// SetWriteConnectionSecretToReference sets the connection-Secret target.
func (mg *ContainerRegistryRepository) SetWriteConnectionSecretToReference(r *xpv1.SecretReference) {
	mg.Spec.WriteConnectionSecretToReference = r
}
