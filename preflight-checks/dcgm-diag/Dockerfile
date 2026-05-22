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

ARG DCGM_VERSION="4.4.2-1-ubuntu22.04"
ARG VERSION="0.1.0"
ARG GIT_COMMIT="none"
ARG BUILD_DATE=$(date -u +%FT%TZ)

FROM public.ecr.aws/docker/library/python:3.13-trixie AS build

ARG VERSION

RUN --mount=type=cache,target=/root/.cache/pip \
    pip install poetry==2.3.3 poetry-plugin-export==1.10.0

ENV POETRY_NO_INTERACTION=1 \
    POETRY_VIRTUALENVS_IN_PROJECT=1 \
    POETRY_VIRTUALENVS_CREATE=1 \
    POETRY_CACHE_DIR=/tmp/poetry_cache

# Mirror the in-repo layout so the poetry path dependency
# `../common/nvsentinel_preflight_runtime` (from pyproject.toml) resolves
# identically in-container and on developers' machines.
COPY preflight-checks/common/ /build/preflight-checks/common/
COPY preflight-checks/dcgm-diag/ /build/preflight-checks/dcgm-diag/
WORKDIR /build/preflight-checks/dcgm-diag

RUN poetry version $(echo "${VERSION}" | sed 's/^v//')
RUN --mount=type=cache,target=/tmp/poetry_cache \
    poetry build --format wheel
RUN --mount=type=cache,target=/tmp/poetry_cache \
    poetry -C /build/preflight-checks/common/nvsentinel_preflight_runtime build --format wheel
RUN poetry export --format requirements.txt --output constraints.txt --without-hashes && \
    sed -i '/^-e file:\/\//d; /^nvsentinel-preflight-runtime/d' constraints.txt


FROM nvcr.io/nvidia/cloud-native/dcgm:${DCGM_VERSION} AS runtime

RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    echo 'Acquire::Retries "3";' > /etc/apt/apt.conf.d/80-retries && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
        apt-transport-https \
        ca-certificates \
        gnupg \
        python3-pip && \
    rm -rf /var/lib/apt/lists/*

ARG PYTHONPATH="/usr/share/datacenter-gpu-manager-4/bindings/python3"

ENV PYTHONPATH=${PYTHONPATH} \
    PYTHONUNBUFFERED=1

COPY --from=build /build/preflight-checks/dcgm-diag/dist/*.whl ./
COPY --from=build /build/preflight-checks/common/nvsentinel_preflight_runtime/dist/*.whl ./
COPY --from=build /build/preflight-checks/dcgm-diag/constraints.txt ./
# Install transitive deps via constraints, then the wheels with --no-deps so
# pip doesn't try to resolve the path-dep file:// URL recorded in the check
# wheel's METADATA (the source path is build-stage-only and absent here).
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install -r constraints.txt && \
    pip install --no-deps ./nvsentinel_preflight_runtime*.whl ./nvsentinel_dcgm_diag*.whl

# Bundle error mapping file (static release data, not runtime config)
COPY distros/kubernetes/nvsentinel/charts/gpu-health-monitor/files/dcgmerrorsmapping.csv /etc/dcgm/dcgmerrorsmapping.csv

RUN groupadd -r nvsentinel && useradd -r -g nvsentinel nvsentinel
USER nvsentinel

ENTRYPOINT ["python3", "-m", "dcgm_diag"]
