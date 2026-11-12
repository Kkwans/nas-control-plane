package httpapi

import (
	// 注册允许比较的摘要算法，避免其可用性依赖其他包的偶然导入。
	_ "crypto/sha256"
	_ "crypto/sha512"
	"strings"

	"github.com/distribution/reference"
)

// dockerArtifactReferenceKey 只比较当前引用存在性，不能证明历史拉取的镜像身份。
// 不补 latest、不丢 tag；与前端共享保守子集，未知/无效引用不可比较。
func dockerArtifactReferenceKey(value string) string {
	if value == "" || strings.TrimSpace(value) != value {
		return ""
	}
	named, err := reference.ParseNormalizedNamed(value)
	if err != nil {
		return ""
	}
	_, tagged := named.(reference.Tagged)
	digested, hasDigest := named.(reference.Digested)
	if !tagged && !hasDigest {
		return ""
	}
	if hasDigest {
		switch digested.Digest().Algorithm().String() {
		case "sha256", "sha384", "sha512":
		default:
			return ""
		}
	}
	return named.String()
}
