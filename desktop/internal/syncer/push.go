package syncer

import (
	"context"
	"math/rand/v2"
	"time"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/core/push"
)

// 断线重连的退避区间。上限取 1 分钟: 邮件客户端可以容忍一分钟的延迟,
// 但不能在服务端维护期间把重连打成一场 DDoS。
const (
	reconnectMinDelay = 1 * time.Second
	reconnectMaxDelay = 60 * time.Second
)

// Run 阻塞运行同步循环, 直到 ctx 被取消。
//
// 每次(重)连成功后都会先做一次全账号同步, 再开始消费推送。这不是多余的:
// EventSource 没有断点续传, 断线期间发生的 StateChange 不会补发, 只靠推送会
// 永久漏掉这段空窗内到达的邮件。重连后的这次同步是唯一的兜底。
func (s *Syncer) Run(ctx context.Context) error {
	delay := reconnectMinDelay

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		if err := s.syncAllAccounts(ctx); err != nil {
			s.log.Error("initial sync failed", "err", err)
		}

		err := s.listen(ctx)
		if ctx.Err() != nil {
			return nil
		}

		// Listen 在流被对端正常关闭时返回 nil, 出错时返回 error。
		// 两种情况都意味着推送已经断了, 必须同等对待, 不能把 nil 当成"正常结束"。
		if err != nil {
			s.log.Warn("push stream dropped", "err", err, "retry_in", delay)
		} else {
			s.log.Info("push stream closed by server", "retry_in", delay)
		}

		if !sleepCtx(ctx, jitter(delay)) {
			return nil
		}

		delay *= 2
		if delay > reconnectMaxDelay {
			delay = reconnectMaxDelay
		}
	}
}

// listen 建立一次 EventSource 连接并消费到断开为止。
func (s *Syncer) listen(ctx context.Context) error {
	stream := &push.EventSource{
		Client: s.client,
		Handler: func(change *jmap.StateChange) {
			s.applyStateChange(ctx, change)
		},
		// 30 秒心跳。中间的反向代理通常在 60 秒空闲后掐连接,
		// 留一半余量, 避免在无新邮件的深夜被反复断开重连。
		Ping: 30,
	}

	// Listen 不接受 context, 只能靠关闭响应体打断。ctx 取消时由这个协程负责唤醒它。
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			stream.Close()
		case <-done:
		}
	}()

	s.log.Info("push stream connected")
	return wrap(CodePush, "event source", stream.Listen())
}

func (s *Syncer) syncAllAccounts(ctx context.Context) error {
	var firstErr error
	for _, accountID := range s.Accounts() {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if err := s.SyncAccount(ctx, accountID); err != nil {
			s.log.Error("sync account", "account", accountID, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// jitter 给退避加上 ±25% 的抖动, 避免多个账号/多台设备在服务端恢复的同一刻齐步重连。
func jitter(d time.Duration) time.Duration {
	spread := float64(d) * 0.25
	return time.Duration(float64(d) - spread + rand.Float64()*2*spread)
}

// sleepCtx 睡到时间到或 ctx 取消, 返回是否正常睡满。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
