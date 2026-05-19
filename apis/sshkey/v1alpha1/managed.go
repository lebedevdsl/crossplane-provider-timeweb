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

// GetCondition returns the matching condition or a zero Condition if none.
func (mg *SSHKey) GetCondition(ct xpv1.ConditionType) xpv1.Condition {
	return mg.Status.GetCondition(ct)
}

// GetDeletionPolicy returns the configured DeletionPolicy.
func (mg *SSHKey) GetDeletionPolicy() xpv1.DeletionPolicy { return mg.Spec.DeletionPolicy }

// GetProviderConfigReference returns the ProviderConfig reference.
func (mg *SSHKey) GetProviderConfigReference() *xpv1.Reference {
	return mg.Spec.ProviderConfigReference
}

// GetPublishConnectionDetailsTo returns the connection-details destination.
func (mg *SSHKey) GetPublishConnectionDetailsTo() *xpv1.PublishConnectionDetailsTo {
	return mg.Spec.PublishConnectionDetailsTo
}

// GetManagementPolicies returns the configured management policies.
func (mg *SSHKey) GetManagementPolicies() xpv1.ManagementPolicies {
	return mg.Spec.ManagementPolicies
}

// GetWriteConnectionSecretToReference returns the connection-Secret target.
func (mg *SSHKey) GetWriteConnectionSecretToReference() *xpv1.SecretReference {
	return mg.Spec.WriteConnectionSecretToReference
}

// SetConditions sets one or more conditions on the resource status.
func (mg *SSHKey) SetConditions(c ...xpv1.Condition) { mg.Status.SetConditions(c...) }

// SetDeletionPolicy sets the DeletionPolicy.
func (mg *SSHKey) SetDeletionPolicy(r xpv1.DeletionPolicy) { mg.Spec.DeletionPolicy = r }

// SetProviderConfigReference sets the ProviderConfig reference.
func (mg *SSHKey) SetProviderConfigReference(r *xpv1.Reference) {
	mg.Spec.ProviderConfigReference = r
}

// SetPublishConnectionDetailsTo sets the connection-details destination.
func (mg *SSHKey) SetPublishConnectionDetailsTo(r *xpv1.PublishConnectionDetailsTo) {
	mg.Spec.PublishConnectionDetailsTo = r
}

// SetManagementPolicies sets the management policies.
func (mg *SSHKey) SetManagementPolicies(r xpv1.ManagementPolicies) {
	mg.Spec.ManagementPolicies = r
}

// SetWriteConnectionSecretToReference sets the connection-Secret target.
func (mg *SSHKey) SetWriteConnectionSecretToReference(r *xpv1.SecretReference) {
	mg.Spec.WriteConnectionSecretToReference = r
}
