package httpapi

import "strings"

const (
	logDedupMaxIDs   = 2048
	logDedupMaxBytes = 256 * 1024
)

// recentLogIDs 是单连接、单 goroutine 的精确 ID FIFO；零值即可使用。
// 只保留克隆后的 ID，不持有日志载荷；固定数组防止队列底层存储持续增长。
type recentLogIDs struct {
	ids                [logDedupMaxIDs]string
	seen               map[string]struct{}
	head, count, bytes int
}

// duplicate 抑制仍在窗口内的重复 ID，不刷新 FIFO 顺序。
// 超过整个字节预算的单个 ID 继续发送但不保留，因此可能重复发送。
// 空 ID 按普通 ID 处理，占一个槽位、零字节。
func (r *recentLogIDs) duplicate(id string) bool {
	if _, ok := r.seen[id]; ok {
		return true
	}
	if len(id) > logDedupMaxBytes {
		return false
	}
	for r.count == logDedupMaxIDs || r.bytes+len(id) > logDedupMaxBytes {
		old := r.ids[r.head]
		delete(r.seen, old)
		r.bytes -= len(old)
		r.ids[r.head] = ""
		r.head = (r.head + 1) % logDedupMaxIDs
		r.count--
	}
	if r.seen == nil {
		r.seen = make(map[string]struct{})
	}
	retained := strings.Clone(id)
	r.ids[(r.head+r.count)%logDedupMaxIDs] = retained
	r.seen[retained] = struct{}{}
	r.count++
	r.bytes += len(retained)
	return false
}
