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

// Package registry maintains the set of preflight checks available to the
// injector. Checks come from two layers: a static layer built once from Helm
// config (cfg.InitContainers), and a dynamic layer populated at runtime from
// PreflightCheck custom resources (see ADR-041). The injector reads the merged
// view on each admission, so dynamically registered checks take effect without
// redeploying NVSentinel.
package registry

import (
	"sort"
	"sync"

	preflightv1alpha1 "github.com/nvidia/nvsentinel/preflight/pkg/apis/preflight/v1alpha1"
	"github.com/nvidia/nvsentinel/preflight/pkg/config"
)

// Registry holds the static and dynamic preflight checks and serves a merged,
// deterministically ordered view. It is safe for concurrent use: the admission
// webhook reads via Checks() while the PreflightCheck controller writes via
// SetDynamic().
type Registry struct {
	mu sync.RWMutex

	// static is the chart-configured check list, in chart order. It never
	// changes after construction.
	static []config.InitContainerSpec

	// dynamic holds checks registered via PreflightCheck CRs, keyed by check
	// name. Replaced wholesale by SetDynamic on each controller resync.
	dynamic map[string]config.InitContainerSpec
}

// New creates a Registry seeded with the static checks from Helm config.
func New(static []config.InitContainerSpec) *Registry {
	return &Registry{
		static:  static,
		dynamic: make(map[string]config.InitContainerSpec),
	}
}

// SetDynamic replaces the dynamic check set with the checks derived from the
// given PreflightCheck CRs. It is called by the controller on every resync.
func (r *Registry) SetDynamic(checks []preflightv1alpha1.PreflightCheck) {
	next := make(map[string]config.InitContainerSpec, len(checks))

	for i := range checks {
		spec := SpecFromCR(&checks[i])
		next[spec.Name] = spec
	}

	r.mu.Lock()
	r.dynamic = next
	r.mu.Unlock()
}

// Checks returns the merged check list: the static checks in chart order,
// followed by dynamic checks (sorted by name for determinism) that do not
// share a name with a static check. A static check therefore always wins over
// a dynamic one with the same name, so a CR cannot silently override a
// chart-defined built-in.
func (r *Registry) Checks() []config.InitContainerSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()

	staticNames := make(map[string]struct{}, len(r.static))

	result := make([]config.InitContainerSpec, 0, len(r.static)+len(r.dynamic))
	for _, s := range r.static {
		staticNames[s.Name] = struct{}{}

		result = append(result, s)
	}

	dynamicNames := make([]string, 0, len(r.dynamic))

	for name := range r.dynamic {
		if _, clash := staticNames[name]; clash {
			continue
		}

		dynamicNames = append(dynamicNames, name)
	}

	sort.Strings(dynamicNames)

	for _, name := range dynamicNames {
		result = append(result, r.dynamic[name])
	}

	return result
}

// SpecFromCR converts a PreflightCheck CR into the config.InitContainerSpec the
// injector consumes. The container name defaults to the check name when unset,
// and DefaultEnabled defaults to false (opt-in) for dynamically registered
// checks.
func SpecFromCR(cr *preflightv1alpha1.PreflightCheck) config.InitContainerSpec {
	container := *cr.Spec.Container.DeepCopy()
	if container.Name == "" {
		container.Name = cr.CheckName()
	}

	defaultEnabled := cr.Spec.DefaultEnabled
	if defaultEnabled == nil {
		// Dynamically registered checks are opt-in by default: they run only
		// when a pod selects them via the preflight-checks annotation.
		off := false
		defaultEnabled = &off
	}

	return config.InitContainerSpec{
		Container:      container,
		DefaultEnabled: defaultEnabled,
	}
}
