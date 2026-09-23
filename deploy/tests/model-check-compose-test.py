#!/usr/bin/env python3
"""Validate the real Compose merge without starting or inspecting any service."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
COMMIT = "1" * 40
ENV = {
    **os.environ,
    "POSTGRES_PASSWORD": "compose-verification-only",
    "MODEL_CHECK_BRIDGE_SECRET": "compose-verification-only-" + "1" * 40,
    "MODEL_CHECK_IMAGE": f"ghcr.io/goldenfishs/sub2api-model-check:sha-{COMMIT}",
    "MODEL_CHECK_CPUS": "2.0",
    "MODEL_CHECK_MEMORY": "1g",
}


def config(files, env=ENV):
    command = ["docker", "compose", "--project-name", "lumivia-model-check-verify", "--env-file", os.devnull]
    for path in files:
        command += ["-f", str(path)]
    result = subprocess.run(command + ["config", "--format", "json"], env=env, capture_output=True, text=True)
    if result.returncode:
        raise AssertionError("Compose validation failed: " + result.stderr)
    return json.loads(result.stdout)


with tempfile.TemporaryDirectory(prefix="model-check-compose-") as directory:
    # Existing operators may already use their own image override. The new
    # companion must not undo those choices or replace storage/services.
    operator_override = Path(directory) / "operator.yml"
    operator_override.write_text(
        "services:\n"
        "  sub2api:\n"
        f"    image: ghcr.io/goldenfishs/sub2api:sha-{COMMIT}\n"
        "    environment:\n"
        "      PRESERVE_OPERATOR_SETTING: existing-value\n"
        "  postgres:\n"
        "    image: postgres:operator-selected\n"
        "  redis:\n"
        "    image: redis:operator-selected\n"
    )
    files = [ROOT / "deploy/docker-compose.yml", operator_override]
    base = config(files)
    merged = config(files + [ROOT / "deploy/docker-compose.model-check.yml"])
    before = base["services"]
    after = merged["services"]
    assert after["postgres"] == before["postgres"], "PostgreSQL configuration changed"
    assert after["redis"] == before["redis"], "Redis configuration changed"
    for field, value in before["sub2api"].items():
        if field == "environment":
            for key, setting in value.items():
                if key in {"MODEL_CHECK_ENABLED", "MODEL_CHECK_UPSTREAM", "MODEL_CHECK_BRIDGE_SECRET"}:
                    continue
                assert after["sub2api"][field].get(key) == setting, f"Existing application setting changed: {key}"
        else:
            assert after["sub2api"].get(field) == value, f"Existing application field changed: {field}"
    for key, volume in base["volumes"].items():
        assert merged["volumes"].get(key) == volume, f"Existing volume changed: {key}"
    assert merged["networks"] == base["networks"], "Existing networks changed"
    app = after["sub2api"]["environment"]
    check = after["model-check"]
    assert app["MODEL_CHECK_ENABLED"] == "true"
    assert app["MODEL_CHECK_UPSTREAM"] == "http://127.0.0.1:8096"
    assert app["MODEL_CHECK_BRIDGE_SECRET"] == check["environment"]["MODEL_CHECK_BRIDGE_SECRET"]
    assert check["image"] == ENV["MODEL_CHECK_IMAGE"]
    assert check["network_mode"] == "service:sub2api"
    assert not check.get("ports") and not check.get("expose") and not check.get("networks")
    assert check["environment"]["MODEL_CHECK_HOST"] == "127.0.0.1"
    assert check["environment"]["MODEL_CHECK_PORT"] == "8096"
    assert check["environment"]["MODEL_CHECK_DEMO"] == "0"
    assert check["environment"]["SUB2API_BACKEND"] == "http://127.0.0.1:8080"
    assert check["environment"]["MODEL_CHECK_SITE_API_BASE"] == "http://127.0.0.1:8080/v1"
    assert check["user"] == "10001:10001" and check["read_only"] and check["init"]
    assert check["depends_on"]["sub2api"]["condition"] == "service_healthy"
    assert check["depends_on"]["sub2api"]["restart"]
    assert check["cap_drop"] == ["ALL"]
    assert "no-new-privileges:true" in check["security_opt"]
    assert float(check["cpus"]) == 2 and int(check["mem_limit"]) == 1024**3
    assert check["memswap_limit"] == check["mem_limit"] and check["pids_limit"] == 256
    assert int(check["shm_size"]) == 256 * 1024**2
    assert any("/tmp:" in mount and "noexec" in mount for mount in check["tmpfs"])
    assert any(v["source"] == "model_check_data" and v["target"] == "/app/data" for v in check["volumes"])
    assert check["logging"]["options"] == {"max-size": "10m", "max-file": "3"}

print("Model-check Compose preserves existing services and enforces the companion boundaries.")
