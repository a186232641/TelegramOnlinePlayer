package broker

import (
	"context"

	"golang.org/x/time/rate"
)

// gate 把所有对 Telegram 的访问收敛到一处:令牌桶限速 + 并发上限串行化,
// 规避单账号高频拉取触发 FLOOD_WAIT(见 §9.4)。退避重试由 gotd 的 floodwait
// 中间件在传输层补充。
type gate struct {
	limiter *rate.Limiter
	sem     chan struct{}
}

func newGate(rps float64, burst, maxConcurrent int) *gate {
	if burst < 1 {
		burst = 1
	}
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &gate{
		limiter: rate.NewLimiter(rate.Limit(rps), burst),
		sem:     make(chan struct{}, maxConcurrent),
	}
}

// do 在取得一个速率令牌并占到一个并发槽后执行 f,执行完释放槽。
// ctx 取消时尽早返回,不执行 f。
func (g *gate) do(ctx context.Context, f func() error) error {
	if err := g.limiter.Wait(ctx); err != nil {
		return err
	}
	select {
	case g.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-g.sem }()
	return f()
}
