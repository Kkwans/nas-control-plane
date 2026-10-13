// Package sqliteconn 为自有 SQLite 连接池在每次建立物理连接时恢复连接级设置。
package sqliteconn

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"

	_ "modernc.org/sqlite"
)

// Open 保留已注册驱动和原始 DSN；pragmas 仅供内部调用方传入固定配置语句。
func Open(dsn string, pragmas ...string) (*sql.DB, error) {
	// 当前 SQLite 驱动的 sql.Open 不建立物理连接，借此获取注册实例。
	probe, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	base := probe.Driver()
	if err := probe.Close(); err != nil {
		return nil, err
	}
	return sql.OpenDB(&connector{
		base: base, dsn: dsn, pragmas: append([]string(nil), pragmas...),
	}), nil
}

type connector struct {
	base    driver.Driver
	dsn     string
	pragmas []string
}

func (c *connector) Driver() driver.Driver { return c.base }

func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 沿用底层 Open 的取消语义；只能在打开前后检查 context。
	conn, err := c.base.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (driver.Conn, error) {
		_ = conn.Close()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	exec, ok := conn.(driver.ExecerContext)
	if !ok {
		return fail(errors.New("sqlite connection does not support context execution"))
	}
	for _, pragma := range c.pragmas {
		if _, err := exec.ExecContext(ctx, pragma, nil); err != nil {
			return fail(err)
		}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	// 返回原始连接，保留 Validator、SessionResetter 等可选接口。
	return conn, nil
}
