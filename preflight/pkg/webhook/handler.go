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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/nvidia/nvsentinel/preflight/pkg/config"
	"github.com/nvidia/nvsentinel/preflight/pkg/gang"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PreflightRunOncePerRayClusterAnnotation names the preflight to skip after
// the owning RayCluster has reached its first fully provisioned state.
const PreflightRunOncePerRayClusterAnnotation = "nvsentinel.nvidia.com/preflight-run-once-per-raycluster"

// GangRegistration is sent to the controller to register a pod with its gang.
type GangRegistration struct {
	Namespace     string
	PodName       string
	GangID        string
	ConfigMapName string
	// CheckNames is a comma-separated list of preflight checks this pod
	// will run. Annotation order when present, chart order for defaults.
	CheckNames string
}

// GangRegistrationFunc is called after a pod is admitted to register it with a gang.
type GangRegistrationFunc func(ctx context.Context, reg GangRegistration)

type Handler struct {
	injector         *Injector
	onGangRegister   GangRegistrationFunc
	rayClusterReader client.Reader
}

func NewHandler(cfg *config.Config, discoverer gang.GangDiscoverer, onGangRegister GangRegistrationFunc, rayClusterReader client.Reader) *Handler {
	return &Handler{
		injector:         NewInjector(cfg, discoverer),
		onGangRegister:   onGangRegister,
		rayClusterReader: rayClusterReader,
	}
}

func (h *Handler) HandleMutate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("Failed to read request body", "error", err)
		http.Error(w, "failed to read request body", http.StatusBadRequest)

		return
	}

	var admissionReview admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &admissionReview); err != nil {
		slog.Error("Failed to unmarshal admission review", "error", err)
		http.Error(w, "failed to unmarshal admission review", http.StatusBadRequest)

		return
	}

	response := h.mutate(r.Context(), admissionReview.Request)
	admissionReview.Response = response

	if admissionReview.Request != nil {
		admissionReview.Response.UID = admissionReview.Request.UID
	}

	respBytes, err := json.Marshal(admissionReview)
	if err != nil {
		slog.Error("Failed to marshal response", "error", err)
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")

	if _, err := w.Write(respBytes); err != nil {
		slog.Error("Failed to write response", "error", err)
	}
}

func (h *Handler) mutate(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	if req == nil {
		return &admissionv1.AdmissionResponse{
			Allowed: true,
		}
	}

	slog.Debug("Processing admission request",
		"namespace", req.Namespace,
		"name", req.Name,
		"operation", req.Operation)

	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		slog.Error("Failed to unmarshal pod", "error", err)

		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: fmt.Sprintf("failed to unmarshal pod: %v", err),
			},
		}
	}

	// Set namespace from request if not in pod spec (common for admission)
	if pod.Namespace == "" {
		pod.Namespace = req.Namespace
	}

	h.omitCompletedRayClusterPreflight(ctx, &pod)

	patch, gangCtx, err := h.injector.InjectInitContainers(&pod)
	if err != nil {
		slog.Error("Failed to inject init containers", "error", err)

		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: fmt.Sprintf("failed to inject init containers: %v", err),
			},
		}
	}

	if patch == nil {
		slog.Debug("No mutation needed", "pod", pod.Name, "namespace", req.Namespace)

		return &admissionv1.AdmissionResponse{
			Allowed: true,
		}
	}

	// Register pod with gang controller if it's part of a gang
	if gangCtx != nil && h.onGangRegister != nil {
		podName := pod.Name
		if podName == "" {
			podName = pod.GenerateName

			slog.Info("Pod name is empty, using generated name", "pod", podName)
		}

		slog.Info("Registering pod with gang",
			"namespace", pod.Namespace,
			"pod", podName,
			"gangID", gangCtx.GangID,
			"configMap", gangCtx.ConfigMapName)

		h.onGangRegister(ctx, GangRegistration{
			Namespace:     pod.Namespace,
			PodName:       podName,
			GangID:        gangCtx.GangID,
			ConfigMapName: gangCtx.ConfigMapName,
			CheckNames:    gangCtx.CheckNames,
		})
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		slog.Error("Failed to marshal patch", "error", err)

		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: fmt.Sprintf("failed to marshal patch: %v", err),
			},
		}
	}

	slog.Info("Mutating pod",
		"namespace", req.Namespace,
		"pod", pod.Name,
		"patchOps", len(patch))

	patchType := admissionv1.PatchTypeJSONPatch

	return &admissionv1.AdmissionResponse{
		Allowed:   true,
		Patch:     patchBytes,
		PatchType: &patchType,
	}
}

func (h *Handler) omitCompletedRayClusterPreflight(ctx context.Context, pod *corev1.Pod) {
	check := pod.Annotations[PreflightRunOncePerRayClusterAnnotation]
	requested, explicitlySelected := pod.Annotations[PreflightChecksAnnotation]
	if check == "" || !explicitlySelected || !h.rayClusterProvisioned(ctx, pod) {
		return
	}

	checks, err := ParseCheckNames(requested)
	if err != nil {
		return // The injector reports the malformed selection.
	}

	remaining := checks[:0]
	for _, requestedCheck := range checks {
		if requestedCheck != check {
			remaining = append(remaining, requestedCheck)
		}
	}
	if len(remaining) == len(checks) {
		return
	}

	pod.Annotations[PreflightChecksAnnotation] = strings.Join(remaining, ",")
	slog.Info("Skipping completed preflight for provisioned RayCluster",
		"namespace", pod.Namespace, "pod", pod.Name, "check", check)
}

func (h *Handler) rayClusterProvisioned(ctx context.Context, pod *corev1.Pod) bool {
	if h.rayClusterReader == nil {
		return false
	}

	owner := metav1.GetControllerOf(pod)
	if owner == nil || owner.APIVersion != "ray.io/v1" || owner.Kind != "RayCluster" {
		return false
	}

	rayCluster := &unstructured.Unstructured{}
	rayCluster.SetAPIVersion(owner.APIVersion)
	rayCluster.SetKind(owner.Kind)
	if h.rayClusterReader.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: owner.Name}, rayCluster) != nil {
		return false
	}

	if rayCluster.GetUID() != owner.UID {
		return false
	}

	conditions, _, _ := unstructured.NestedSlice(rayCluster.Object, "status", "conditions")
	for _, rawCondition := range conditions {
		condition, ok := rawCondition.(map[string]any)
		if ok && condition["type"] == "RayClusterProvisioned" && condition["status"] == "True" {
			return true
		}
	}

	return false
}
