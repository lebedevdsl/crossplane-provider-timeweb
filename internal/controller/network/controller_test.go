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

package network

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	networkv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/network/v1alpha1"
)

func TestMapNetworkToRouters(t *testing.T) { // T006
	ctx := context.Background()

	withSelector := &networkv1alpha1.Router{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "edge-selector"},
		Spec: networkv1alpha1.RouterSpec{ForProvider: networkv1alpha1.RouterParameters{
			Networks: []networkv1alpha1.RouterNetworkAttachment{{
				NetworkSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "true"}},
			}},
		}},
	}
	explicitOnly := &networkv1alpha1.Router{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "edge-explicit"},
		Spec: networkv1alpha1.RouterSpec{ForProvider: networkv1alpha1.RouterParameters{
			Networks: []networkv1alpha1.RouterNetworkAttachment{{NetworkID: strPtr("network-aaa")}},
		}},
	}
	otherNamespace := &networkv1alpha1.Router{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-b", Name: "edge-other"},
		Spec: networkv1alpha1.RouterSpec{ForProvider: networkv1alpha1.RouterParameters{
			Networks: []networkv1alpha1.RouterNetworkAttachment{{
				NetworkSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "true"}},
			}},
		}},
	}

	k := fake.NewClientBuilder().WithScheme(refsScheme(t)).
		WithObjects(withSelector, explicitOnly, otherNamespace).Build()

	changed := &networkv1alpha1.Network{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "n1", Labels: map[string]string{"app": "true"}},
	}

	reqs := mapNetworkToRouters(k)(ctx, changed)

	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1 (only the selector-using router in team-a): %v", len(reqs), reqs)
	}
	if reqs[0].Namespace != "team-a" || reqs[0].Name != "edge-selector" {
		t.Errorf("got %s/%s, want team-a/edge-selector", reqs[0].Namespace, reqs[0].Name)
	}
}
