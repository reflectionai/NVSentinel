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
	"testing"

	"github.com/nvidia/nvsentinel/preflight/pkg/gang/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGangAnnotations(t *testing.T) {
	t.Run("waits for every peer IP", func(t *testing.T) {
		annotations, ready, err := gangAnnotations(&types.GangInfo{
			GangID:           "gang",
			ExpectedMinCount: 2,
			Peers: []types.PeerInfo{
				{PodName: "worker-0", PodIP: "10.0.0.1"},
				{PodName: "worker-1"},
			},
		}, "preflight-nccl-allreduce", 29500)
		require.NoError(t, err)
		assert.False(t, ready)
		assert.Nil(t, annotations)
	})

	t.Run("builds one canonical sorted snapshot", func(t *testing.T) {
		annotations, ready, err := gangAnnotations(&types.GangInfo{
			GangID:           "gang/full",
			ExpectedMinCount: 2,
			Peers: []types.PeerInfo{
				{
					PodName:    "worker-1",
					PodIP:      "10.0.0.2",
					CheckNames: "preflight-nccl-allreduce,preflight-dcgm-diag",
				},
				{PodName: "worker-0", PodIP: "10.0.0.1"},
			},
		}, "preflight-nccl-allreduce", 29500)
		require.NoError(t, err)
		require.True(t, ready)
		assert.Equal(t, "2", annotations[types.GangExpectedCountAnnotation])
		assert.Equal(t, "10.0.0.1", annotations[types.GangMasterAddrAnnotation])
		assert.Equal(t, "29500", annotations[types.GangMasterPortAnnotation])
		assert.Equal(t, "gang/full", annotations[types.GangIDAnnotation])
		assert.Equal(t,
			"worker-0;10.0.0.1;0;preflight-nccl-allreduce\n"+
				"worker-1;10.0.0.2;1;preflight-nccl-allreduce,preflight-dcgm-diag",
			annotations[types.GangPeersAnnotation])
	})

	t.Run("rejects more addressable peers than expected", func(t *testing.T) {
		_, ready, err := gangAnnotations(&types.GangInfo{
			GangID:           "gang",
			ExpectedMinCount: 1,
			Peers: []types.PeerInfo{
				{PodName: "worker-0", PodIP: "10.0.0.1"},
				{PodName: "worker-1", PodIP: "10.0.0.2"},
			},
		}, "preflight-nccl-allreduce", 29500)
		require.Error(t, err)
		assert.False(t, ready)
	})
}
