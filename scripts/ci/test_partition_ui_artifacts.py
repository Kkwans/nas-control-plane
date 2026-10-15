"""仅使用临时文件的制品分片回归，不启动浏览器或服务。"""

import hashlib
from pathlib import Path
import tempfile
import re
import subprocess
import sys
import unittest
from unittest.mock import patch

from partition_ui_artifacts import MAX_PARTS, PART_BYTES, partition


class PartitionTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        root = Path(self.temporary.name)
        self.source = root / "test-results"
        self.source.mkdir()
        self.destination = root / "parts"

    def write(self, name, data):
        path = self.source / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
        return path

    def manifest(self, root):
        return {
            path.relative_to(root).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest()
            for path in root.rglob("*") if path.is_file()
        }

    def test_boundary_and_path_order_preserve_every_selected_byte(self):
        # 故意逆序创建：输出必须仅由路径排序与大小决定。
        self.write("z/ui-captures/c.png", b"12345")
        self.write("a/ui-captures/b-capture-state.json", b"67890")
        self.write("a/ui-captures/a.png", b"abcde")
        original = self.manifest(self.source)
        self.assertEqual(partition(self.source, self.destination, part_bytes=10), 2)
        self.assertEqual(self.manifest(self.destination / "part-1"), {
            name: digest for name, digest in original.items() if name.startswith("a/")
        })
        combined = {}
        for part in self.destination.iterdir():
            self.assertLessEqual(sum(p.stat().st_size for p in part.rglob("*") if p.is_file()), 10)
            combined.update(self.manifest(part))
        self.assertEqual(combined, original)
        self.assertEqual(self.manifest(self.source), original)
        repeat = self.destination.with_name("repeat")
        self.assertEqual(partition(self.source, repeat, part_bytes=10), 2)
        self.assertEqual(self.manifest(repeat), self.manifest(self.destination))

    def test_allowlist_excludes_other_outputs_and_hidden_files(self):
        allowed = "run/ui-captures/desktop.png"
        for name in [allowed, "run/ui-captures/auth.json", "run/trace.zip",
                     "run/other/screenshot.png", "run/ui-captures/nested/extra.png",
                     "run/ui-captures/.hidden.png", ".private/ui-captures/secret.png"]:
            self.write(name, b"content")
        self.assertEqual(partition(self.source, self.destination), 1)
        self.assertEqual(list(self.manifest(self.destination / "part-1")), [allowed])

    def test_single_oversize_fails_before_copying(self):
        self.write("run/ui-captures/a.png", b"12345")
        with self.assertRaisesRegex(ValueError, "单个证据"):
            partition(self.source, self.destination, part_bytes=4)
        self.assertFalse(self.destination.exists())

    def test_sixteen_parts_are_allowed_but_seventeenth_fails_without_partial_copy(self):
        self.assertEqual(MAX_PARTS, 16)
        for number in range(MAX_PARTS):
            self.write(f"run/ui-captures/{number}.png", b"12")
        self.assertEqual(partition(self.source, self.destination, part_bytes=2), MAX_PARTS)
        self.write(f"run/ui-captures/{MAX_PARTS}.png", b"12")
        destination = self.destination.with_name("overflow")
        with self.assertRaisesRegex(ValueError, f"{MAX_PARTS} 个分片"):
            partition(self.source, destination, part_bytes=2)
        self.assertFalse(destination.exists())

    def test_many_empty_files_are_bounded_by_zip_metadata(self):
        for number in range(2 * MAX_PARTS + 1):
            self.write(f"run/ui-captures/{number:02d}.png", b"")
        # 缩小元数据预算，验证零字节文件仍会拆分并触发分片数量上限。
        with patch("partition_ui_artifacts.METADATA_BYTES", 2400):
            with self.assertRaisesRegex(ValueError, f"{MAX_PARTS} 个分片"):
                partition(self.source, self.destination)
        self.assertFalse(self.destination.exists())

    def test_workflow_upload_slots_match_partition_limit(self):
        workflow = Path(__file__).resolve().parents[2] / ".github/workflows/ci.yml"
        steps = re.findall(
            r"^      - name: Upload fixture UI screenshots part (\d+)\n"
            r"((?:^        .*\n|^\n)*)",
            workflow.read_text(encoding="utf-8"),
            flags=re.MULTILINE,
        )
        self.assertEqual([int(number) for number, _ in steps], list(range(1, MAX_PARTS + 1)))
        for number, body in steps:
            with self.subTest(part=number):
                self.assertIn(
                    "        if: ${{ always() && steps.prepare-ui-artifacts.outcome == 'success' "
                    "&& fromJSON(steps.prepare-ui-artifacts.outputs.parts) >= " + number + " }}\n",
                    body,
                )
                for line in [
                    "        uses: actions/upload-artifact@v4",
                    "          name: ncp-fixture-ui-${{ github.sha }}-${{ github.run_attempt }}-part-" + number,
                    "          path: ${{ runner.temp }}/ncp-ui-artifacts/part-" + number + "/",
                    "          if-no-files-found: error",
                    "          retention-days: 14",
                ]:
                    self.assertIn(line + "\n", body)

    def test_symlink_directory_is_not_followed(self):
        outside = self.source.parent / "outside"
        outside.mkdir()
        (outside / "secret.png").write_bytes(b"private")
        (self.source / "ui-captures").symlink_to(outside, target_is_directory=True)
        self.assertEqual(partition(self.source, self.destination), 0)
        self.assertEqual(list(self.destination.iterdir()), [])

    def test_real_budget_boundary(self):
        path = self.write("run/ui-captures/a.png", b"")
        with path.open("wb") as output:
            output.truncate(PART_BYTES)
        self.assertEqual(partition(self.source, self.destination), 1)
        self.assertEqual((self.destination / "part-1" / path.relative_to(self.source)).stat().st_size, PART_BYTES)

    def test_symlink_file_is_rejected(self):
        target = self.write("auth.json", b"private")
        path = self.source / "run/ui-captures/a.png"
        path.parent.mkdir(parents=True)
        path.symlink_to(target)
        with self.assertRaisesRegex(ValueError, "符号链接"):
            partition(self.source, self.destination)
        self.assertFalse(self.destination.exists())

    def test_empty_or_missing_source_has_no_parts(self):
        self.assertEqual(partition(self.source / "missing", self.destination), 0)
        self.assertEqual(list(self.destination.iterdir()), [])

    def test_cli_writes_numeric_github_output(self):
        self.write("run/ui-captures/a.png", b"png")
        output = self.source.parent / "github-output"
        result = subprocess.run([
            sys.executable, str(Path(__file__).with_name("partition_ui_artifacts.py")),
            "--source", str(self.source), "--destination", str(self.destination),
            "--github-output", str(output),
        ], capture_output=True, text=True, check=False, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(output.read_text(), "parts=1\n")

    def test_stale_or_nested_destination_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "源目录"):
            partition(self.source, self.source / "parts")
        self.destination.mkdir()
        with self.assertRaisesRegex(ValueError, "已存在"):
            partition(self.source, self.destination)


if __name__ == "__main__":
    unittest.main()
