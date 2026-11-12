package openapi_test

import (
	"github.com/getkin/kin-openapi/openapi3"
	"strings"
	"testing"
)

func TestJobQueueTimeoutContract(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	job := doc.Components.Schemas["JobSnapshot"].Value
	description := job.Properties["errorCode"].Value.Description
	for _, text := range []string{"JOB_QUEUE_TIMEOUT", "10 分钟", "failed", "排队等待超时，任务未开始执行", "镜像拉取", "Compose", "镜像删除", "创建记录耗时"} {
		if !strings.Contains(description, text) {
			t.Fatalf("missing %q", text)
		}
	}
	if !strings.Contains(job.Properties["persistenceStatus"].Value.Description, "未开始") {
		t.Fatal("pending ignores unstarted results")
	}
}
