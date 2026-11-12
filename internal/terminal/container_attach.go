package terminal

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"

	"github.com/moby/moby/client"
)

// 此 adapter 只用于已完成版本协商且由 gateway 独占的 SDK client。
// Attach 使用独立 client，普通 HTTP 连接池不参与握手连接的归属。
type containerSDK struct {
	*client.Client
	version string
}

func (api *containerSDK) ExecAttach(ctx context.Context, id string, options client.ExecAttachOptions) (client.ExecAttachResult, error) {
	return attachContainerExec(ctx, api.version, id, options, (&net.Dialer{}).DialContext)
}

type containerAttachConn struct {
	net.Conn
	once     sync.Once
	closeErr error
	wrote    atomic.Bool
}

func (c *containerAttachConn) Close() error {
	c.once.Do(func() { c.closeErr = c.Conn.Close() })
	return c.closeErr
}

func (c *containerAttachConn) Write(p []byte) (int, error) {
	// 即便 Write 返回错误，也不能证明 daemon 没有收到启动请求。
	c.wrote.Store(true)
	return c.Conn.Write(p)
}

type containerAttachCloseWriter struct{ *containerAttachConn }

func (c *containerAttachCloseWriter) CloseWrite() error {
	return c.Conn.(client.CloseWriter).CloseWrite()
}

// SDK 调用保持同步；取消仅关闭本次取得的连接。所有出口 join 回调，
// 成功时仍交还 SDK 的 Conn/Reader，保留两层缓冲及可选 CloseWrite。
func attachContainerExec(ctx context.Context, version, id string, options client.ExecAttachOptions, dial func(context.Context, string, string) (net.Conn, error)) (result client.ExecAttachResult, err error) {
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if version == "" || id == "" {
		return result, errors.New("container attach requires negotiated version and exec ID")
	}
	var owned *containerAttachConn
	var stop func()
	api, err := client.New(client.WithHost(localDockerHost), client.WithAPIVersion(version), client.WithDialContext(func(ctx context.Context, network, address string) (net.Conn, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		conn, dialErr := dial(ctx, network, address)
		if conn == nil {
			if dialErr == nil {
				dialErr = errors.New("container attach dial returned no connection")
			}
			return nil, dialErr
		}
		owned = &containerAttachConn{Conn: conn}
		stop = onContextAbort(ctx, func() { _ = owned.Close() })
		if err := errors.Join(dialErr, ctx.Err()); err != nil {
			return nil, err
		}
		if _, ok := conn.(client.CloseWriter); ok {
			return &containerAttachCloseWriter{owned}, nil
		}
		return owned, nil
	}))
	if err != nil {
		return result, err
	}
	defer api.Close()
	result, err = api.ExecAttach(ctx, id, options)
	if stop != nil {
		stop()
	}
	err = errors.Join(err, ctx.Err())
	if err != nil {
		if owned != nil {
			err = errors.Join(err, owned.Close())
			// 仅标记本片可识别的取消中断；SDK 普通非 101 错误没有
			// 结构化状态来源，保留既有 shell 探测回退行为。
			if ctx.Err() != nil && owned.wrote.Load() {
				err = errors.Join(err, ErrContainerExitUnconfirmed)
			}
		}
		return client.ExecAttachResult{}, err
	}
	return result, nil
}
