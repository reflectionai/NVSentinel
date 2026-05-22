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

"""Shared runtime bootstrap for NVSentinel preflight checks.

Every preflight check container that runs as a Python process should call
`bootstrap()` once at the very top of `main()` before any other work. It
configures structured JSON logging, discovers the runtime context this
process is running in (rank, pod, node), binds that context onto structlog
so every subsequent log line carries it, and emits a "Worker started"
event so post-hoc analysis (e.g., Datadog Logs queries) can identify which
ranks/pods got as far as starting before a failure.

Usage:

    from nvsentinel_preflight_runtime import bootstrap

    def main() -> int:
        runtime = bootstrap(
            module="preflight-nccl-allreduce",
            version="0.1.0",
        )
        log.info("about to do thing")  # @rank, @pod_name etc. injected automatically
        ...
"""

from nvsentinel_preflight_runtime.runtime import PreflightRuntime, bootstrap

__all__ = ['PreflightRuntime', 'bootstrap']
