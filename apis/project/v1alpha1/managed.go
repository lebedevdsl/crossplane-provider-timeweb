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

import xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"

// Project satisfies the crossplane-runtime v2 ModernManaged interface.

func (mg *Project) GetCondition(ct xpv2.ConditionType) xpv2.Condition {
	return mg.Status.GetCondition(ct)
}

func (mg *Project) SetConditions(c ...xpv2.Condition) { mg.Status.SetConditions(c...) }

func (mg *Project) GetProviderConfigReference() *xpv2.ProviderConfigReference {
	return mg.Spec.ProviderConfigReference
}

func (mg *Project) SetProviderConfigReference(r *xpv2.ProviderConfigReference) {
	mg.Spec.ProviderConfigReference = r
}

func (mg *Project) GetManagementPolicies() xpv2.ManagementPolicies {
	return mg.Spec.ManagementPolicies
}

func (mg *Project) SetManagementPolicies(r xpv2.ManagementPolicies) {
	mg.Spec.ManagementPolicies = r
}

func (mg *Project) GetWriteConnectionSecretToReference() *xpv2.LocalSecretReference {
	return mg.Spec.WriteConnectionSecretToReference
}

func (mg *Project) SetWriteConnectionSecretToReference(r *xpv2.LocalSecretReference) {
	mg.Spec.WriteConnectionSecretToReference = r
}
