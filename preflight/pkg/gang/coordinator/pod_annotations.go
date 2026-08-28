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

package coordinator

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/nvidia/nvsentinel/preflight/pkg/gang/types"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PublishPodAnnotations writes one complete gang snapshot onto pod. The
// webhook projects these annotations as the same files used by the ConfigMap
// transport, so preflight check images do not need a second reader.
//
// The bool is false while peers are still acquiring IPs. Callers should
// requeue the pod; once the gang is ready, each pod needs exactly one patch.
func (c *Coordinator) PublishPodAnnotations(
	ctx context.Context,
	pod *corev1.Pod,
	gangInfo *types.GangInfo,
	checkNames string,
) (bool, error) {
	annotations, ready, err := gangAnnotations(gangInfo, checkNames, c.config.MasterPort)
	if err != nil || !ready {
		return ready, err
	}
	foundPod := false
	for _, peer := range gangInfo.Peers {
		if peer.PodName == pod.Name && peer.PodIP != "" {
			foundPod = true
			break
		}
	}
	if !foundPod {
		return false, fmt.Errorf("pod %s/%s is absent from ready gang %q",
			pod.Namespace, pod.Name, gangInfo.GangID)
	}

	updated := pod.DeepCopy()
	if updated.Annotations == nil {
		updated.Annotations = make(map[string]string, len(annotations))
	}
	changed := false
	for key, value := range annotations {
		if updated.Annotations[key] == value {
			continue
		}
		updated.Annotations[key] = value
		changed = true
	}
	if !changed {
		return true, nil
	}

	if err := c.client.Patch(ctx, updated, client.MergeFrom(pod.DeepCopy())); err != nil {
		return false, fmt.Errorf("failed to publish gang annotations to pod %s/%s: %w",
			pod.Namespace, pod.Name, err)
	}
	return true, nil
}

func gangAnnotations(
	gangInfo *types.GangInfo,
	checkNames string,
	masterPort int,
) (map[string]string, bool, error) {
	if gangInfo.ExpectedMinCount < 1 {
		return nil, false, nil
	}

	peers := make([]types.PeerInfo, 0, len(gangInfo.Peers))
	for _, peer := range gangInfo.Peers {
		if peer.PodIP != "" {
			peers = append(peers, peer)
		}
	}
	if len(peers) < gangInfo.ExpectedMinCount {
		return nil, false, nil
	}
	if len(peers) > gangInfo.ExpectedMinCount {
		return nil, false, fmt.Errorf("gang %q has %d addressable peers for expected count %d",
			gangInfo.GangID, len(peers), gangInfo.ExpectedMinCount)
	}

	sort.Slice(peers, func(i, j int) bool {
		return peers[i].PodName < peers[j].PodName
	})
	lines := make([]string, 0, len(peers))
	for rank, peer := range peers {
		peerCheckNames := peer.CheckNames
		if peerCheckNames == "" {
			peerCheckNames = checkNames
		}
		lines = append(lines, fmt.Sprintf("%s;%s;%d;%s", peer.PodName, peer.PodIP, rank, peerCheckNames))
	}

	return map[string]string{
		types.GangExpectedCountAnnotation: strconv.Itoa(gangInfo.ExpectedMinCount),
		types.GangPeersAnnotation:         strings.Join(lines, "\n"),
		types.GangMasterAddrAnnotation:    peers[0].PodIP,
		types.GangMasterPortAnnotation:    strconv.Itoa(masterPort),
		types.GangIDAnnotation:            gangInfo.GangID,
	}, true, nil
}
