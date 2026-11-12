package httpapi

import (
	"math"
	"strings"
	"unicode/utf8"
)

const (
	pullProgressMaxLayers      = 128
	pullProgressMaxIDBytes     = 128
	pullProgressMaxStatusBytes = 256
	pullProgressTextBudget     = 32 << 10
	// JSON 字符串最坏六倍转义，另留层结构、数字及进度标志空间；不含任务其他字段。
	pullProgressJSONBudget = 220 << 10
)

// boundedPullLayer 只在 viewMu 内调用。每层预留完整 status 配额，让已接纳层始终可更新。
// 拒绝 ID 不截短、不缓存；接纳文本独立复制，避免小切片保留上游巨型字符串。
func boundedPullLayer(j *jobSnapshot, layer jobLayer) (jobLayer, bool) {
	if layer.ID == "" {
		layer.ID = "status"
	}
	if len(layer.ID) > pullProgressMaxIDBytes || !utf8.ValidString(layer.ID) {
		j.ProgressLimited = true
		return jobLayer{}, false
	}
	if _, exists := j.Layers[layer.ID]; !exists {
		reserved := 2*len(layer.ID) + pullProgressMaxStatusBytes
		for id := range j.Layers {
			reserved += 2*len(id) + pullProgressMaxStatusBytes
		}
		if len(j.Layers) >= pullProgressMaxLayers || reserved > pullProgressTextBudget {
			j.ProgressLimited = true
			return jobLayer{}, false
		}
	}
	end := 0
	for end < len(layer.Status) && end < pullProgressMaxStatusBytes {
		runeValue, size := utf8.DecodeRuneInString(layer.Status[end:])
		if (runeValue == utf8.RuneError && size == 1) || end+size > pullProgressMaxStatusBytes {
			break
		}
		end += size
	}
	if end != len(layer.Status) {
		j.ProgressLimited = true
	}
	layer.ID = strings.Clone(layer.ID)
	layer.Status = strings.Clone(layer.Status[:end])
	return layer, true
}

// 遥测聚合饱和，不能因恶意极值溢出为负数。
func addPullBytes(a, b int64) int64 {
	if b <= 0 {
		return a
	}
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}
