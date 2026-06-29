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

// --- Network ----------------------------------------------------------------

func (mg *Network) GetCondition(ct xpv2.ConditionType) xpv2.Condition {
	return mg.Status.GetCondition(ct)
}

func (mg *Network) SetConditions(c ...xpv2.Condition) { mg.Status.SetConditions(c...) }

func (mg *Network) GetProviderConfigReference() *xpv2.ProviderConfigReference {
	return mg.Spec.ProviderConfigReference
}

func (mg *Network) SetProviderConfigReference(r *xpv2.ProviderConfigReference) {
	mg.Spec.ProviderConfigReference = r
}

func (mg *Network) GetManagementPolicies() xpv2.ManagementPolicies {
	return mg.Spec.ManagementPolicies
}

func (mg *Network) SetManagementPolicies(r xpv2.ManagementPolicies) {
	mg.Spec.ManagementPolicies = r
}

func (mg *Network) GetWriteConnectionSecretToReference() *xpv2.LocalSecretReference {
	return mg.Spec.WriteConnectionSecretToReference
}

func (mg *Network) SetWriteConnectionSecretToReference(r *xpv2.LocalSecretReference) {
	mg.Spec.WriteConnectionSecretToReference = r
}

// --- FloatingIP -------------------------------------------------------------

func (mg *FloatingIP) GetCondition(ct xpv2.ConditionType) xpv2.Condition {
	return mg.Status.GetCondition(ct)
}

func (mg *FloatingIP) SetConditions(c ...xpv2.Condition) { mg.Status.SetConditions(c...) }

func (mg *FloatingIP) GetProviderConfigReference() *xpv2.ProviderConfigReference {
	return mg.Spec.ProviderConfigReference
}

func (mg *FloatingIP) SetProviderConfigReference(r *xpv2.ProviderConfigReference) {
	mg.Spec.ProviderConfigReference = r
}

func (mg *FloatingIP) GetManagementPolicies() xpv2.ManagementPolicies {
	return mg.Spec.ManagementPolicies
}

func (mg *FloatingIP) SetManagementPolicies(r xpv2.ManagementPolicies) {
	mg.Spec.ManagementPolicies = r
}

func (mg *FloatingIP) GetWriteConnectionSecretToReference() *xpv2.LocalSecretReference {
	return mg.Spec.WriteConnectionSecretToReference
}

func (mg *FloatingIP) SetWriteConnectionSecretToReference(r *xpv2.LocalSecretReference) {
	mg.Spec.WriteConnectionSecretToReference = r
}

// --- Router -------------------------------------------------------------

func (mg *Router) GetCondition(ct xpv2.ConditionType) xpv2.Condition {
	return mg.Status.GetCondition(ct)
}

func (mg *Router) SetConditions(c ...xpv2.Condition) { mg.Status.SetConditions(c...) }

func (mg *Router) GetProviderConfigReference() *xpv2.ProviderConfigReference {
	return mg.Spec.ProviderConfigReference
}

func (mg *Router) SetProviderConfigReference(r *xpv2.ProviderConfigReference) {
	mg.Spec.ProviderConfigReference = r
}

func (mg *Router) GetManagementPolicies() xpv2.ManagementPolicies {
	return mg.Spec.ManagementPolicies
}

func (mg *Router) SetManagementPolicies(r xpv2.ManagementPolicies) {
	mg.Spec.ManagementPolicies = r
}

func (mg *Router) GetWriteConnectionSecretToReference() *xpv2.LocalSecretReference {
	return mg.Spec.WriteConnectionSecretToReference
}

func (mg *Router) SetWriteConnectionSecretToReference(r *xpv2.LocalSecretReference) {
	mg.Spec.WriteConnectionSecretToReference = r
}

// --- Firewall -----------------------------------------------------------

func (mg *Firewall) GetCondition(ct xpv2.ConditionType) xpv2.Condition {
	return mg.Status.GetCondition(ct)
}

func (mg *Firewall) SetConditions(c ...xpv2.Condition) { mg.Status.SetConditions(c...) }

func (mg *Firewall) GetProviderConfigReference() *xpv2.ProviderConfigReference {
	return mg.Spec.ProviderConfigReference
}

func (mg *Firewall) SetProviderConfigReference(r *xpv2.ProviderConfigReference) {
	mg.Spec.ProviderConfigReference = r
}

func (mg *Firewall) GetManagementPolicies() xpv2.ManagementPolicies {
	return mg.Spec.ManagementPolicies
}

func (mg *Firewall) SetManagementPolicies(r xpv2.ManagementPolicies) {
	mg.Spec.ManagementPolicies = r
}

func (mg *Firewall) GetWriteConnectionSecretToReference() *xpv2.LocalSecretReference {
	return mg.Spec.WriteConnectionSecretToReference
}

func (mg *Firewall) SetWriteConnectionSecretToReference(r *xpv2.LocalSecretReference) {
	mg.Spec.WriteConnectionSecretToReference = r
}
