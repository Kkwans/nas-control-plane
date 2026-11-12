"""执行真实 workflow shell，验证历史 tag 兼容分支不会跳过当前检查。"""

import os
from pathlib import Path
import subprocess
import tempfile
import textwrap
import unittest


WORKFLOW = Path(__file__).resolve().parents[2] / ".github/workflows/ci.yml"


def workflow_script(name):
    # 仅提取命名 step 的多行 run，不引入额外 YAML 依赖。
    text = WORKFLOW.read_text()
    step = text.split(f"      - name: {name}\n", 1)[1].split("      - name:", 1)[0]
    return textwrap.dedent(step.split("        run: |\n", 1)[1])


class WorkflowBackfillTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        (self.root / "frontend").mkdir()
        self.scripts = self.root / "scripts/ci"
        self.scripts.mkdir(parents=True)
        self.output = self.root / "github-output"

    def run_step(self, name, event="push", tag=""):
        return subprocess.run(
            ["bash", "-eo", "pipefail", "-c", workflow_script(name)],
            cwd=self.root / "frontend",
            env={**os.environ, "GITHUB_EVENT_NAME": event, "NCP_RELEASE_TAG": tag,
                 "RUNNER_TEMP": str(self.root), "GITHUB_OUTPUT": str(self.output)},
            capture_output=True, text=True,
        )

    def install_scripts(self, fail=False):
        (self.scripts / "partition_ui_artifacts.py").write_text(
            "import os\nfrom pathlib import Path\n"
            + ("raise SystemExit(7)\n" if fail else
               "Path(os.environ['GITHUB_OUTPUT']).write_text('parts=2\\n')\n")
        )
        (self.scripts / "test_partition_ui_artifacts.py").write_text(
            "import unittest\nclass Tests(unittest.TestCase):\n"
            f"    def test_result(self): self.assertTrue({not fail})\n"
        )

    def test_historical_absent_scripts_skip_with_zero_parts(self):
        for step in ("Test UI artifact partitioning", "Prepare bounded fixture UI artifacts"):
            result = self.run_step(step, "workflow_dispatch", "2026.10.10-v2")
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("Historical release tag", result.stdout)
        self.assertEqual(self.output.read_text(), "parts=0\n")

    def test_current_missing_scripts_fail(self):
        for event, tag in (("push", ""), ("pull_request", ""),
                           ("workflow_dispatch", ""), ("push", "2026.10.10-v2")):
            for step in ("Test UI artifact partitioning", "Prepare bounded fixture UI artifacts"):
                with self.subTest(event=event, tag=tag, step=step):
                    self.assertNotEqual(self.run_step(step, event, tag).returncode, 0)
        self.assertFalse(self.output.exists())

    def test_historical_partial_scripts_fail(self):
        (self.scripts / "partition_ui_artifacts.py").write_text("")
        for step in ("Test UI artifact partitioning", "Prepare bounded fixture UI artifacts"):
            self.assertNotEqual(self.run_step(step, "workflow_dispatch", "v1").returncode, 0)
        self.assertFalse(self.output.exists())

    def test_web_regression_absent_only_skips_historical_tag(self):
        step = "Web artifact regression"
        result = self.run_step(step, "workflow_dispatch", "v1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Historical release tag", result.stdout)
        for event, tag in (("push", ""), ("workflow_dispatch", ""), ("push", "v1")):
            self.assertNotEqual(self.run_step(step, event, tag).returncode, 0)

    def test_web_regression_present_always_executes(self):
        # 用命令替身观察真实 shell 路径，无需 Python 测试依赖 Node。
        scripts = self.root / "frontend/scripts"
        scripts.mkdir()
        (scripts / "verify-web-assets.test.mjs").write_text("")
        bin_dir = self.root / "bin"
        bin_dir.mkdir()
        node = bin_dir / "node"
        node.write_text("#!/bin/sh\nexit 7\n")
        node.chmod(0o755)
        from unittest.mock import patch
        with patch.dict(os.environ, {"PATH": str(bin_dir) + os.pathsep + os.environ["PATH"]}):
            for event, tag in (("push", ""), ("workflow_dispatch", "v1")):
                self.assertEqual(self.run_step("Web artifact regression", event, tag).returncode, 7)

    def test_present_scripts_run_even_for_backfill(self):
        self.install_scripts()
        for event, tag in (("push", ""), ("workflow_dispatch", "v1")):
            for step in ("Test UI artifact partitioning", "Prepare bounded fixture UI artifacts"):
                result = self.run_step(step, event, tag)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertNotIn("Historical release tag", result.stdout)
            self.assertEqual(self.output.read_text(), "parts=2\n")

    def test_present_script_failures_not_masked(self):
        self.install_scripts(fail=True)
        for event, tag in (("push", ""), ("workflow_dispatch", "v1")):
            for step in ("Test UI artifact partitioning", "Prepare bounded fixture UI artifacts"):
                self.assertNotEqual(self.run_step(step, event, tag).returncode, 0)
        self.assertFalse(self.output.exists())


if __name__ == "__main__":
    unittest.main()
