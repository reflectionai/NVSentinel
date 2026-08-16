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

package webhook

import (
	"testing"

	preflightv1alpha1 "github.com/nvidia/nvsentinel/preflight/pkg/apis/preflight/v1alpha1"
	"github.com/nvidia/nvsentinel/preflight/pkg/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// A check registered dynamically via a PreflightCheck CR is injectable through
// the registry-backed injector without changing the static chart config.
func TestInjector_DynamicCheckSelectableByAnnotation(t *testing.T) {
	cfg := testConfig() // static: preflight-dcgm-diag

	reg := registry.New(cfg.InitContainers)
	reg.SetDynamic([]preflightv1alpha1.PreflightCheck{{
		ObjectMeta: metav1.ObjectMeta{Name: "bandwidth-check"},
		Spec: preflightv1alpha1.PreflightCheckSpec{
			Container: corev1.Container{Image: "myregistry/bandwidth-check:v1"},
		},
	}})

	injector := NewInjectorWithRegistry(cfg, nil, reg)

	pod := gpuPod()
	pod.Annotations = map[string]string{PreflightChecksAnnotation: "bandwidth-check"}

	selected, err := injector.selectInitContainers(pod)
	require.NoError(t, err)
	require.Len(t, selected, 1)
	assert.Equal(t, "bandwidth-check", selected[0].Name)
	assert.Equal(t, "myregistry/bandwidth-check:v1", selected[0].Image)
}

// A dynamic check defaults to opt-in: it does not run when the pod has no
// annotation, even though the static default check does.
func TestInjector_DynamicCheckOptInByDefault(t *testing.T) {
	cfg := testConfig() // static preflight-dcgm-diag is defaultEnabled (nil => true)

	reg := registry.New(cfg.InitContainers)
	reg.SetDynamic([]preflightv1alpha1.PreflightCheck{{
		ObjectMeta: metav1.ObjectMeta{Name: "bandwidth-check"},
		Spec: preflightv1alpha1.PreflightCheckSpec{
			Container: corev1.Container{Image: "myregistry/bandwidth-check:v1"},
		},
	}})

	injector := NewInjectorWithRegistry(cfg, nil, reg)

	// No annotation: only the default-enabled static check runs.
	selected, err := injector.selectInitContainers(gpuPod())
	require.NoError(t, err)
	require.Len(t, selected, 1)
	assert.Equal(t, "preflight-dcgm-diag", selected[0].Name)
}

// An annotation naming a check that is neither static nor dynamically
// registered is rejected, and the error lists the available checks.
func TestInjector_UnknownDynamicCheckRejected(t *testing.T) {
	cfg := testConfig()
	reg := registry.New(cfg.InitContainers)
	injector := NewInjectorWithRegistry(cfg, nil, reg)

	pod := gpuPod()
	pod.Annotations = map[string]string{PreflightChecksAnnotation: "does-not-exist"}

	_, err := injector.selectInitContainers(pod)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown checks")
}
