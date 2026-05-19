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

// crossplane-runtime's `resource.Managed` interface requires these accessor
// methods on every MR type. The boilerplate is identical across resources;
// each MR package carries its own copy because the embedded
// `xpv1.ResourceSpec` / `xpv1.ResourceStatus` don't expose pointer receivers
// for the surrounding outer type.

// GetCondition returns the matching condition or a zero Condition if none.
func (mg *Project) GetCondition(ct xpv1.ConditionType) xpv1.Condition {
	return mg.Status.GetCondition(ct)
}

// GetDeletionPolicy returns the configured DeletionPolicy.
func (mg *Project) GetDeletionPolicy() xpv1.DeletionPolicy { return mg.Spec.DeletionPolicy }

// GetProviderConfigReference returns the ProviderConfig reference.
func (mg *Project) GetProviderConfigReference() *xpv1.Reference {
	return mg.Spec.ProviderConfigReference
}

// GetPublishConnectionDetailsTo returns the connection-details destination.
func (mg *Project) GetPublishConnectionDetailsTo() *xpv1.PublishConnectionDetailsTo {
	return mg.Spec.PublishConnectionDetailsTo
}

// GetManagementPolicies returns the configured management policies.
func (mg *Project) GetManagementPolicies() xpv1.ManagementPolicies {
	return mg.Spec.ManagementPolicies
}

// GetWriteConnectionSecretToReference returns the connection-Secret target.
func (mg *Project) GetWriteConnectionSecretToReference() *xpv1.SecretReference {
	return mg.Spec.WriteConnectionSecretToReference
}

// SetConditions sets one or more conditions on the resource status.
func (mg *Project) SetConditions(c ...xpv1.Condition) { mg.Status.SetConditions(c...) }

// SetDeletionPolicy sets the DeletionPolicy.
func (mg *Project) SetDeletionPolicy(r xpv1.DeletionPolicy) { mg.Spec.DeletionPolicy = r }

// SetProviderConfigReference sets the ProviderConfig reference.
func (mg *Project) SetProviderConfigReference(r *xpv1.Reference) {
	mg.Spec.ProviderConfigReference = r
}

// SetPublishConnectionDetailsTo sets the connection-details destination.
func (mg *Project) SetPublishConnectionDetailsTo(r *xpv1.PublishConnectionDetailsTo) {
	mg.Spec.PublishConnectionDetailsTo = r
}

// SetManagementPolicies sets the management policies.
func (mg *Project) SetManagementPolicies(r xpv1.ManagementPolicies) {
	mg.Spec.ManagementPolicies = r
}

// SetWriteConnectionSecretToReference sets the connection-Secret target.
func (mg *Project) SetWriteConnectionSecretToReference(r *xpv1.SecretReference) {
	mg.Spec.WriteConnectionSecretToReference = r
}
