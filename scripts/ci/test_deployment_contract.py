"""验证密钥挂载检查本身，避免缺失文件被静默转换成目录。"""

import copy
import unittest

from check_deployment_contract import validate


class DeploymentContractTests(unittest.TestCase):
    def setUp(self):
        self.config = {"services": {"server": {
            "command": ["serve", "--database-key", "/run/secrets/test.key"],
            "volumes": [{"type": "bind", "target": "/run/secrets/test.key",
                         "read_only": True, "bind": {"create_host_path": False}}],
        }}}

    def test_valid(self):
        validate(self.config)

    def test_missing_or_unsafe_mount_rejected(self):
        for patch in ({"type": "volume"}, {"read_only": False},
                      {"bind": {"create_host_path": True}}):
            with self.subTest(patch=patch):
                config = copy.deepcopy(self.config)
                config["services"]["server"]["volumes"][0].update(patch)
                with self.assertRaises(ValueError):
                    validate(config)

    def test_normalized_false_can_be_omitted(self):
        self.config["services"]["server"]["volumes"][0]["bind"] = {}
        validate(self.config)

    def test_wrong_target_rejected(self):
        self.config["services"]["server"]["volumes"][0]["target"] = "/wrong.key"
        with self.assertRaises(ValueError):
            validate(self.config)

    def test_duplicate_mount_rejected(self):
        mounts = self.config["services"]["server"]["volumes"]
        mounts.append(copy.deepcopy(mounts[0]))
        with self.assertRaises(ValueError):
            validate(self.config)


if __name__ == "__main__":
    unittest.main()
