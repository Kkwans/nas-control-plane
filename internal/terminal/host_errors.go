package terminal

import "errors"

// ErrHostExitUnconfirmed 表示未取得所拥有直属 child 的终止回收确认。
var ErrHostExitUnconfirmed = errors.New("host child exit unconfirmed")

// ErrHostCleanupUnconfirmed 表示预算内未能确认本地 I/O 与取消回调收尾。
var ErrHostCleanupUnconfirmed = errors.New("host local cleanup unconfirmed")
