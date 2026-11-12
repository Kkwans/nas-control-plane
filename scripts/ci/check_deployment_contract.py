"""检查 Compose 规范化输出中的首次部署密钥挂载约束。"""

import json
import sys


def validate(config):
    server = config["services"]["server"]
    command = server["command"]
    index = command.index("--database-key")
    target = command[index + 1]
    mounts = [item for item in server["volumes"] if item["target"] == target]
    if len(mounts) != 1:
        raise ValueError("数据库密钥必须有且仅有一个挂载")
    mount = mounts[0]
    if mount.get("type") != "bind" or mount.get("read_only") is not True:
        raise ValueError("数据库密钥必须通过只读 bind 挂载")
    # Compose 的规范化 JSON 可以省略值为 false 的字段。
    if mount.get("bind", {}).get("create_host_path", False) is not False:
        raise ValueError("数据库密钥缺失时必须拒绝挂载，不能自动创建目录")


if __name__ == "__main__":
    validate(json.load(sys.stdin))
    print("deployment key mount contract: passed")
