// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PreflightCheckSpec defines a single preflight check that the injector may add
// to matching GPU pods. Registering a PreflightCheck makes the check available
// dynamically, without redeploying NVSentinel; see ADR-041.
type PreflightCheckSpec struct {
	// DefaultEnabled controls whether this check runs when a pod has no
	// preflight-checks annotation. It defaults to false so that dynamically
	// registered checks are opt-in (via the per-pod annotation) until an
	// operator trusts them. See ADR-034 for per-pod selection.
	// +optional
	DefaultEnabled *bool `json:"defaultEnabled,omitempty"`

	// GangAware indicates that the check participates in gang coordination
	// (multi-node collectives). Informational for now; mirrors the built-in
	// gang-aware checks.
	// +optional
	GangAware bool `json:"gangAware,omitempty"`

	// Container is the init container injected for this check. If its name is
	// empty, the PreflightCheck object's name is used as the check (and
	// container) name, which is what the per-pod selection annotation matches.
	// +kubebuilder:validation:Required
	Container corev1.Container `json:"container"`
}

// PreflightCheckStatus defines the observed state of a PreflightCheck.
type PreflightCheckStatus struct {
	// ObservedGeneration is the most recent generation observed by the
	// preflight controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=pfc
// +kubebuilder:printcolumn:name="Image",type="string",JSONPath=".spec.container.image"
// +kubebuilder:printcolumn:name="DefaultEnabled",type="boolean",JSONPath=".spec.defaultEnabled"
// +kubebuilder:printcolumn:name="GangAware",type="boolean",JSONPath=".spec.gangAware"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// PreflightCheck is the Schema for dynamically registered preflight checks.
type PreflightCheck struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PreflightCheckSpec   `json:"spec,omitempty"`
	Status PreflightCheckStatus `json:"status,omitempty"`
}

// CheckName returns the name used to identify this check in the per-pod
// selection annotation and in gang validation: the container name if set,
// otherwise the object name.
func (c *PreflightCheck) CheckName() string {
	if c.Spec.Container.Name != "" {
		return c.Spec.Container.Name
	}

	return c.Name
}

// +kubebuilder:object:root=true

// PreflightCheckList contains a list of PreflightCheck.
type PreflightCheckList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PreflightCheck `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PreflightCheck{}, &PreflightCheckList{})
}
