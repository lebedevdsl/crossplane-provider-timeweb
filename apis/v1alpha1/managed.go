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
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
)

// Accessor methods required by crossplane-runtime's `resource.ProviderConfig`
// and `resource.ProviderConfigUsage` interfaces. Embedded structs alone don't
// expose these on the outer type, so each is forwarded explicitly.

// GetCondition returns the matching condition on the ProviderConfig status.
func (pc *ProviderConfig) GetCondition(ct xpv1.ConditionType) xpv1.Condition {
	return pc.Status.GetCondition(ct)
}

// SetConditions applies one or more conditions to the ProviderConfig status.
func (pc *ProviderConfig) SetConditions(c ...xpv1.Condition) { pc.Status.SetConditions(c...) }

// GetUsers returns the number of managed resources currently bound to this
// ProviderConfig.
func (pc *ProviderConfig) GetUsers() int64 { return pc.Status.Users }

// SetUsers stores the number of managed resources currently bound to this
// ProviderConfig.
func (pc *ProviderConfig) SetUsers(n int64) { pc.Status.Users = n }

// GetProviderConfigReference returns the ProviderConfig reference of this
// usage record (required by crossplane-runtime's ProviderConfigUsage iface).
func (pcu *ProviderConfigUsage) GetProviderConfigReference() xpv1.Reference {
	return pcu.ProviderConfigReference
}

// SetProviderConfigReference stores the ProviderConfig reference.
func (pcu *ProviderConfigUsage) SetProviderConfigReference(r xpv1.Reference) {
	pcu.ProviderConfigReference = r
}

// GetResourceReference returns the typed reference to the managed resource
// that bound to the ProviderConfig.
func (pcu *ProviderConfigUsage) GetResourceReference() xpv1.TypedReference {
	return pcu.ResourceReference
}

// SetResourceReference stores the typed reference to the managed resource.
func (pcu *ProviderConfigUsage) SetResourceReference(r xpv1.TypedReference) {
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
