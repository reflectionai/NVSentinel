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

"""Tests for nvsentinel_preflight_runtime.runtime."""

import json
import logging
from collections.abc import Iterator

import pytest
import structlog
from nvsentinel_preflight_runtime import runtime


@pytest.fixture(autouse=True)
def _isolate_logging_and_contextvars() -> Iterator[None]:
  """Reset structlog contextvars and root-logger handlers between tests."""
  structlog.contextvars.clear_contextvars()
  root = logging.getLogger()
  prior_handlers = root.handlers[:]
  prior_level = root.level
  yield
  structlog.contextvars.clear_contextvars()
  for h in root.handlers[:]:
    root.removeHandler(h)
  for h in prior_handlers:
    root.addHandler(h)
  root.setLevel(prior_level)


def _drain(capsys: pytest.CaptureFixture[str]) -> list[dict[str, object]]:
  """Parse each captured stderr line as JSON and return the list."""
  out = capsys.readouterr()
  return [json.loads(line) for line in out.err.splitlines() if line.strip()]


def _clear_env(monkeypatch: pytest.MonkeyPatch) -> None:
  """Drop every env var the bootstrap reads, so each test sets only what it cares about."""
  for var in (
    'RANK',
    'LOCAL_RANK',
    'WORLD_SIZE',
    'POD_NAME',
    'NODE_NAME',
    'LOG_LEVEL',
  ):
    monkeypatch.delenv(var, raising=False)


def test_full_torchrun_environment(
  monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
  """All torchrun vars + pod/node vars populate every PreflightRuntime field."""
  _clear_env(monkeypatch)
  monkeypatch.setenv('RANK', '5')
  monkeypatch.setenv('LOCAL_RANK', '5')
  monkeypatch.setenv('WORLD_SIZE', '16')
  monkeypatch.setenv('POD_NAME', 'amr-xxx-worker-abc')
  monkeypatch.setenv('NODE_NAME', 'gke-node-foo')

  rt = runtime.bootstrap(module='preflight-x', version='0.1.0')

  assert rt.rank == 5
  assert rt.local_rank == 5
  assert rt.world_size == 16
  assert rt.pod_name == 'amr-xxx-worker-abc'
  assert rt.node_name == 'gke-node-foo'
  assert rt.module == 'preflight-x'
  assert rt.version == '0.1.0'

  # Worker-started event carries every discovered field via contextvars.
  events = _drain(capsys)
  assert len(events) == 1, events
  started = events[0]
  assert started['event'] == 'Worker started'
  assert started['rank'] == 5
  assert started['local_rank'] == 5
  assert started['world_size'] == 16
  assert started['pod_name'] == 'amr-xxx-worker-abc'
  assert started['node_name'] == 'gke-node-foo'
  assert started['module'] == 'preflight-x'
  assert started['version'] == '0.1.0'


def test_subsequent_logs_inherit_contextvars(
  monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
  """Logs emitted after bootstrap() inherit rank/pod_name automatically."""
  _clear_env(monkeypatch)
  monkeypatch.setenv('RANK', '0')
  monkeypatch.setenv('WORLD_SIZE', '8')
  monkeypatch.setenv('POD_NAME', 'pod-zzz')

  runtime.bootstrap(module='preflight-x', version='0.1.0')
  logging.getLogger(__name__).error(
    'something failed', extra={'detail': 'boom'}
  )

  events = _drain(capsys)
  # bootstrap emits one event; our error emits another.
  assert len(events) == 2
  failed = events[1]
  assert failed['event'] == 'something failed'
  # Contextvars merged into the error event without explicit extra=.
  assert failed['rank'] == 0
  assert failed['world_size'] == 8
  assert failed['pod_name'] == 'pod-zzz'
  assert failed['detail'] == 'boom'


def test_single_process_check_no_torchrun_env(
  monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
  """Single-process checks (dcgm-diag) leave rank/world_size as None."""
  _clear_env(monkeypatch)
  monkeypatch.setenv('POD_NAME', 'pod-yyy')

  rt = runtime.bootstrap(module='preflight-dcgm-diag', version='0.1.0')

  assert rt.rank is None
  assert rt.local_rank is None
  assert rt.world_size is None
  assert rt.pod_name == 'pod-yyy'
  assert rt.node_name is None

  started = _drain(capsys)[0]
  assert 'rank' not in started
  assert 'world_size' not in started
  assert started['pod_name'] == 'pod-yyy'


def test_empty_env_returns_all_none(
  monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
  """Bootstrap with no env vars still succeeds; emits a bare Worker started."""
  _clear_env(monkeypatch)

  rt = runtime.bootstrap(module='preflight-x', version='0.1.0')

  assert rt == runtime.PreflightRuntime(
    module='preflight-x',
    version='0.1.0',
    rank=None,
    local_rank=None,
    world_size=None,
    pod_name=None,
    node_name=None,
  )
  started = _drain(capsys)[0]
  assert started['event'] == 'Worker started'


def test_malformed_int_env_treated_as_missing(
  monkeypatch: pytest.MonkeyPatch,
) -> None:
  """A non-integer RANK is treated as missing rather than raising."""
  _clear_env(monkeypatch)
  monkeypatch.setenv('RANK', 'not-a-number')
  monkeypatch.setenv('POD_NAME', 'pod-zzz')

  rt = runtime.bootstrap(module='preflight-x', version='0.1.0')

  assert rt.rank is None
  assert rt.pod_name == 'pod-zzz'


def test_log_level_from_env(monkeypatch: pytest.MonkeyPatch) -> None:
  """Bootstrap honors LOG_LEVEL when `level` arg is omitted."""
  _clear_env(monkeypatch)
  monkeypatch.setenv('LOG_LEVEL', 'debug')

  runtime.bootstrap(module='preflight-x', version='0.1.0')

  assert logging.getLogger().level == logging.DEBUG


def test_explicit_level_overrides_env(monkeypatch: pytest.MonkeyPatch) -> None:
  """An explicit `level` arg wins over LOG_LEVEL."""
  _clear_env(monkeypatch)
  monkeypatch.setenv('LOG_LEVEL', 'debug')

  runtime.bootstrap(module='preflight-x', version='0.1.0', level='warning')

  assert logging.getLogger().level == logging.WARNING
