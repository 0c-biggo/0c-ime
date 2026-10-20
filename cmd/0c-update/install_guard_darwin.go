//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"syscall"
	"time"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/internal/autoupdate"
)

// prepareMacInstall checks every supported endpoint before draining any engine.
// A disconnected/stale socket is harmless; an inaccessible or unresponsive
// engine is unknown, and must not be mistaken for an idle engine.
func prepareMacInstall(ctx context.Context, dialers []autoupdate.Dialer) error {
	var writers []int
	for _, dial := range dialers {
		pid, err := macEngineIdle(dial)
		if err != nil {
			return fmt.Errorf("%w：%v", autoupdate.ErrShellBusy, err)
		}
		if pid != 0 {
			writers = append(writers, pid)
		}
	}
	for _, dial := range dialers {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w：%v", autoupdate.ErrShellBusy, err)
		}
		if err := drainMacEngine(ctx, dial); err != nil {
			return fmt.Errorf("%w：%v", autoupdate.ErrShellBusy, err)
		}
	}
	// Older engines release the socket before their final save. Keep supporting
	// those installations by waiting for the observed writer to actually exit.
	for _, pid := range writers {
		if err := waitMacWriter(ctx, pid); err != nil {
			return fmt.Errorf("%w：%v", autoupdate.ErrShellBusy, err)
		}
	}
	return nil
}

func waitMacWriter(ctx context.Context, pid int) error {
	deadline := time.Now().Add(15 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil && !errors.Is(err, syscall.EPERM) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("原引擎尚未完成存檔退出")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func drainMacEngine(ctx context.Context, dial autoupdate.Dialer) error {
	conn, err := dial(2 * time.Second)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil
		}
		return err
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	err = ipc.WriteFrame(conn, ipc.Envelope{Op: ipc.OpDrain})
	var reply ipc.Reply
	if err == nil {
		err = ipc.ReadFrame(conn, &reply)
		if err == nil && !reply.OK {
			err = fmt.Errorf("交接未完成：%s", reply.Error)
		}
	}
	conn.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	// The engine keeps the endpoint until learning has been saved. A lost reply
	// alone is not proof of handoff; wait for release, with a bounded deadline.
	deadline := time.Now().Add(15 * time.Second)
	for {
		conn, err := dial(time.Second)
		if err != nil {
			if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
				return nil
			}
			return err
		}
		conn.Close()
		if time.Now().After(deadline) {
			return fmt.Errorf("引擎尚未完成存檔交接")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func macEngineIdle(dial autoupdate.Dialer) (int, error) {
	conn, err := dial(2 * time.Second)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			return 0, nil
		}
		return 0, fmt.Errorf("無法確認引擎狀態：%w", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return 0, err
	}
	if err := ipc.WriteFrame(conn, ipc.Envelope{Op: ipc.OpHealth}); err != nil {
		return 0, err
	}
	var reply ipc.Reply
	if err := ipc.ReadFrame(conn, &reply); err != nil {
		return 0, err
	}
	if !reply.OK {
		return 0, fmt.Errorf("無法確認引擎狀態：%s", reply.Error)
	}
	count, err := strconv.Atoi(reply.Info["composing"])
	if err != nil || count < 0 {
		return 0, fmt.Errorf("引擎未回報有效組字狀態")
	}
	if count != 0 {
		return 0, fmt.Errorf("請先完成組字，再完成更新")
	}
	pid, err := strconv.Atoi(reply.Info["pid"])
	if err != nil || pid <= 0 || pid > 2147483647 {
		return 0, fmt.Errorf("引擎未回報有效寫入者狀態")
	}
	return pid, nil
}
