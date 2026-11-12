package database

import (
	"context"
	"sync"
)

// 保守固定默认值，不代表 NAS 实测容量。仅约束同一 Manager 的池拥有操作。
const maxActiveDatabaseOperations = 4

// 无等待队列，避免无截止时间的调用堆积；零值可用。
type operationBudget struct {
	mu     sync.Mutex
	active int
}

func (b *operationBudget) acquire(ctx context.Context, sourceID string) (func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, MapError(ctx, Source{ID: sourceID}, "admission", err)
	}
	if b.active >= maxActiveDatabaseOperations {
		return nil, newDatabaseError(CodeOperationBusy, Source{ID: sourceID}, "admission", nil)
	}
	b.active++
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.active--
			b.mu.Unlock()
		})
	}, nil
}
