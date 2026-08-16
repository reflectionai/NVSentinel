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

package registry

import (
	"testing"

	preflightv1alpha1 "github.com/nvidia/nvsentinel/preflight/pkg/apis/preflight/v1alpha1"
	"github.com/nvidia/nvsentinel/preflight/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func staticSpec(name string) config.InitContainerSpec {
	return config.InitContainerSpec{
		Container: corev1.Container{Name: name, Image: "static/" + name},
	}
}

func cr(name, image string, defaultEnabled *bool) preflightv1alpha1.PreflightCheck {
	return preflightv1alpha1.PreflightCheck{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: preflightv1alpha1.PreflightCheckSpec{
			DefaultEnabled: defaultEnabled,
			Container:      corev1.Container{Image: image},
		},
	}
}

func names(specs []config.InitContainerSpec) []string {
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = s.Name
	}

	return out
}

func TestChecks_StaticOnly(t *testing.T) {
	r := New([]config.InitContainerSpec{staticSpec("a"), staticSpec("b")})
	assert.Equal(t, []string{"a", "b"}, names(r.Checks()))
}

func TestChecks_StaticPlusDynamicSortedAndAppended(t *testing.T) {
	r := New([]config.InitContainerSpec{staticSpec("dcgm"), staticSpec("nccl")})
	r.SetDynamic([]preflightv1alpha1.PreflightCheck{
		cr("zeta", "img/zeta", nil),
		cr("alpha", "img/alpha", nil),
	})

	// Static first in chart order, then dynamic sorted by name.
	assert.Equal(t, []string{"dcgm", "nccl", "alpha", "zeta"}, names(r.Checks()))
}

func TestChecks_StaticWinsOnNameClash(t *testing.T) {
	r := New([]config.InitContainerSpec{staticSpec("dcgm")})
	r.SetDynamic([]preflightv1alpha1.PreflightCheck{cr("dcgm", "img/override", nil)})

	checks := r.Checks()
	require.Len(t, checks, 1)
	assert.Equal(t, "dcgm", checks[0].Name)
	// The static image is preserved; the CR cannot override a built-in.
	assert.Equal(t, "static/dcgm", checks[0].Image)
}

func TestSetDynamic_ReplacesPreviousSet(t *testing.T) {
	r := New(nil)
	r.SetDynamic([]preflightv1alpha1.PreflightCheck{cr("a", "img/a", nil)})
	r.SetDynamic([]preflightv1alpha1.PreflightCheck{cr("b", "img/b", nil)})

	assert.Equal(t, []string{"b"}, names(r.Checks()))
}

func TestSpecFromCR_Defaults(t *testing.T) {
	// No container name and no defaultEnabled -> name from object, opt-in off.
	spec := SpecFromCR(&preflightv1alpha1.PreflightCheck{
		ObjectMeta: metav1.ObjectMeta{Name: "bandwidth-check"},
		Spec:       preflightv1alpha1.PreflightCheckSpec{Container: corev1.Container{Image: "img/bw"}},
	})

	assert.Equal(t, "bandwidth-check", spec.Name)
	require.NotNil(t, spec.DefaultEnabled)
	assert.False(t, *spec.DefaultEnabled, "dynamic checks must be opt-in by default")
	assert.False(t, spec.IsDefaultEnabled())
}

func TestSpecFromCR_ExplicitNameAndEnabled(t *testing.T) {
	on := true
	spec := SpecFromCR(&preflightv1alpha1.PreflightCheck{
		ObjectMeta: metav1.ObjectMeta{Name: "obj-name"},
		Spec: preflightv1alpha1.PreflightCheckSpec{
			DefaultEnabled: &on,
			Container:      corev1.Container{Name: "container-name", Image: "img/x"},
		},
	})

	// Container name wins over object name when both are set.
	assert.Equal(t, "container-name", spec.Name)
	assert.True(t, spec.IsDefaultEnabled())
}
