# Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Runtime context discovery and structlog context binding.

This module is responsible for the very first work a preflight check
process does: configure the structured logger, discover where in the
distributed gang this process sits (rank/local_rank/world_size set by
torchrun) and which pod/node it runs on (POD_NAME/NODE_NAME set by the
webhook/downward API), bind every discovered field as a structlog
contextvar so every subsequent log line carries it, and emit a single
"Worker started" event so post-hoc analyzers can identify which workers
made it past spawn before any failure.

The single entrypoint is `bootstrap()`. Checks that don't run under
torchrun (single-process-per-pod checks like dcgm-diag) get a degraded
runtime where rank/local_rank/world_size are None and are skipped from
context binding — pod_name/node_name still bind, so the per-pod
attribution still works.
"""

import dataclasses
import logging
import os
from typing import Final

import structlog

from nvsentinel_preflight_runtime import logger as logger_lib


@dataclasses.dataclass(frozen=True, slots=True)
class PreflightRuntime:
  """Runtime context discovered at process start.

  Attributes:
    module: The check module name (e.g. "preflight-nccl-allreduce").
    version: The check version string.
    rank: Global rank (0 ≤ rank < world_size) when running under torchrun;
      None otherwise.
    local_rank: GPU index on this node (0 ≤ local_rank < nproc_per_node)
      when running under torchrun; None otherwise.
    world_size: Total number of processes in the gang when running under
      torchrun; None otherwise.
    pod_name: Kubernetes pod name from the POD_NAME env var (set by the
      NVSentinel webhook via the downward API); None if unset.
    node_name: Kubernetes node name from NODE_NAME; None if unset.
  """

  module: str
  version: str
  rank: int | None
  local_rank: int | None
  world_size: int | None
  pod_name: str | None
  node_name: str | None


_TORCHRUN_RANK_ENV: Final[str] = 'RANK'
_TORCHRUN_LOCAL_RANK_ENV: Final[str] = 'LOCAL_RANK'
_TORCHRUN_WORLD_SIZE_ENV: Final[str] = 'WORLD_SIZE'
_POD_NAME_ENV: Final[str] = 'POD_NAME'
_NODE_NAME_ENV: Final[str] = 'NODE_NAME'


def _read_int_env(name: str) -> int | None:
  """Read an integer from env; return None on missing or non-integer values."""
  raw = os.environ.get(name)
  if raw is None or raw == '':
    return None
  try:
    return int(raw)
  except ValueError:
    return None


def _read_str_env(name: str) -> str | None:
  """Read a non-empty string from env; return None if unset or empty."""
  raw = os.environ.get(name)
  return raw or None


def bootstrap(
  *,
  module: str,
  version: str,
  level: str | None = None,
) -> PreflightRuntime:
  """Initialize logging, discover runtime, bind context, emit start event.

  Should be called exactly once at the top of `main()`, before any other
  work. After this call, every log line emitted by the process automatically
  carries the discovered runtime context (rank, pod_name, etc.).

  Args:
    module: Check module name, e.g. "preflight-nccl-allreduce".
    version: Check version string.
    level: Log level. If None, reads LOG_LEVEL env (default "info").

  Returns:
    The discovered `PreflightRuntime`. Callers that need to read rank
    explicitly can use the return value; callers that only emit logs can
    ignore it (context-binding is already in effect).
  """
  effective_level = (
    level if level is not None else os.environ.get('LOG_LEVEL', 'info')
  )
  logger_lib.set_default_structured_logger(module, version, effective_level)

  runtime = PreflightRuntime(
    module=module,
    version=version,
    rank=_read_int_env(_TORCHRUN_RANK_ENV),
    local_rank=_read_int_env(_TORCHRUN_LOCAL_RANK_ENV),
    world_size=_read_int_env(_TORCHRUN_WORLD_SIZE_ENV),
    pod_name=_read_str_env(_POD_NAME_ENV),
    node_name=_read_str_env(_NODE_NAME_ENV),
  )

  context: dict[str, int | str] = {}
  if runtime.rank is not None:
    context['rank'] = runtime.rank
  if runtime.local_rank is not None:
    context['local_rank'] = runtime.local_rank
  if runtime.world_size is not None:
    context['world_size'] = runtime.world_size
  if runtime.pod_name is not None:
    context['pod_name'] = runtime.pod_name
  if runtime.node_name is not None:
    context['node_name'] = runtime.node_name
  structlog.contextvars.bind_contextvars(**context)

  log = logging.getLogger(__name__)
  log.info('Worker started')

  return runtime
