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

package controller

import (
	"context"
	"fmt"
	"log/slog"

	preflightv1alpha1 "github.com/nvidia/nvsentinel/preflight/pkg/apis/preflight/v1alpha1"
	"github.com/nvidia/nvsentinel/preflight/pkg/registry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PreflightCheckController watches PreflightCheck custom resources and keeps the
// injector's dynamic check set in sync (see ADR-041). On any change it performs
// a full resync: it lists every PreflightCheck and replaces the registry's
// dynamic layer. A full resync keeps the reconcile idempotent and avoids having
// to reason about individual add/update/delete deltas.
type PreflightCheckController struct {
	client.Client
	registry *registry.Registry
}

// NewPreflightCheckController creates a controller that feeds the registry.
func NewPreflightCheckController(c client.Client, reg *registry.Registry) *PreflightCheckController {
	return &PreflightCheckController{Client: c, registry: reg}
}

// SetupWithManager registers the controller with the manager.
func (c *PreflightCheckController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&preflightv1alpha1.PreflightCheck{}).
		Complete(c)
}

// Reconcile re-lists all PreflightCheck objects and rebuilds the dynamic
// registry layer. The request is ignored beyond triggering the resync.
func (c *PreflightCheckController) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	var list preflightv1alpha1.PreflightCheckList
	if err := c.List(ctx, &list); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list PreflightChecks: %w", err)
	}

	c.registry.SetDynamic(list.Items)

	slog.Info("Synced dynamic preflight checks", "count", len(list.Items))

	return ctrl.Result{}, nil
}
