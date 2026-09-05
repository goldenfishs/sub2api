"""Exercise deployment failure paths without touching Docker or production."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / "lumivia-update.sh"
IMAGE = "ghcr.io/goldenfishs/sub2api:sha-" + "a" * 40
DOCKER = r'''#!/usr/bin/env bash
set -eu
echo "$*" >> "$CALLS"
case "$*" in
  "pull "*) [[ "${FAIL_AT:-}" != pull ]] ;;
  "compose ps -q sub2api") echo app ;;
  "compose ps -q postgres") echo db ;;
  "commit "*) echo snapshot ;;
  "exec db sh -c "*) [[ "${FAIL_AT:-}" != backup ]] && echo dump ;;
  "inspect "*)
    if [[ "${FAIL_AT:-}" == health ]]; then echo unhealthy; else echo healthy; fi ;;
esac
'''


class DeploymentTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        (self.root / "data").mkdir()
        (self.root / "data" / "config").write_text("fixture")
        (self.root / "docker-compose.yml").write_text("services: {}\n")
        (self.root / ".env").write_text("TEST=fixture\n")
        bindir = self.root / "bin"
        bindir.mkdir()
        for name, content in {"docker": DOCKER, "flock": "#!/bin/sh\nexit 0\n", "sleep": "#!/bin/sh\nexit 0\n"}.items():
            path = bindir / name
            path.write_text(content)
            path.chmod(0o755)
        self.calls = self.root / "calls"
        self.env = dict(os.environ, PATH=str(bindir) + ":" + os.environ["PATH"], CALLS=str(self.calls))

    def run_update(self, image=IMAGE, fail=""):
        return subprocess.run(["bash", str(SCRIPT), image], cwd=self.root,
                              env=dict(self.env, FAIL_AT=fail), capture_output=True, text=True)

    def test_rejects_official_and_floating_images(self):
        for image in ("weishaw/sub2api:latest", "ghcr.io/goldenfishs/sub2api:lumivia-latest"):
            self.assertNotEqual(self.run_update(image).returncode, 0)
        self.assertFalse(self.calls.exists())

    def test_preserves_unmanaged_override(self):
        override = self.root / "docker-compose.override.yml"
        override.write_text("custom configuration\n")
        self.assertNotEqual(self.run_update().returncode, 0)
        self.assertEqual(override.read_text(), "custom configuration\n")
        self.assertFalse(self.calls.exists())

    def test_failed_pull_does_not_stop_application(self):
        self.assertNotEqual(self.run_update(fail="pull").returncode, 0)
        self.assertNotIn("compose stop", self.calls.read_text())

    def test_backup_failure_restores_current_container_snapshot(self):
        self.assertNotEqual(self.run_update(fail="backup").returncode, 0)
        override = (self.root / "docker-compose.override.yml").read_text()
        self.assertIn("sub2api-lumivia-rollback:", override)
        self.assertIn("compose up -d --no-deps --pull never --force-recreate sub2api", self.calls.read_text())

    def test_health_failure_restores_snapshot(self):
        self.assertNotEqual(self.run_update(fail="health").returncode, 0)
        self.assertIn("sub2api-lumivia-rollback:", (self.root / "docker-compose.override.yml").read_text())

    def test_success_pins_image_and_creates_backup_before_restart(self):
        result = self.run_update()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(IMAGE, (self.root / "docker-compose.override.yml").read_text())
        backups = list((self.root / "backups").iterdir())
        self.assertEqual(len(backups), 1)
        for name in ("database.dump", "database.contents", "data.tar.gz", ".env", "rollback-image"):
            self.assertTrue((backups[0] / name).exists(), name)
        calls = self.calls.read_text()
        self.assertLess(calls.index("commit app"), calls.index("compose stop sub2api"))
        self.assertLess(calls.index("pg_dump"), calls.index("compose up"))


if __name__ == "__main__":
    unittest.main()
