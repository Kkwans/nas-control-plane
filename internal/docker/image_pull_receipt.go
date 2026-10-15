package docker

import "errors"

// ValidateImagePullResult 只信任本次规范化请求的完成回执，不推断仓库别名或镜像存在状态。
func ValidateImagePullResult(request ImagePullRequest, result ImagePullResult) error {
	reference, err := normalizeImageReference(request.Reference)
	if err != nil || !result.Completed || result.Reference != reference {
		return errors.New("image pull completion does not match request")
	}
	return nil
}
