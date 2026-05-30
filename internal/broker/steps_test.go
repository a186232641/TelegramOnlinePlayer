package broker

import (
	"context"
	"testing"
	"time"
)

func TestStepStoreLifecycle(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	s := newStepStore(5*time.Minute, clock)

	tok, err := s.create(&loginStep{Phone: "+8613800000000", PhoneCodeHash: "hash1"})
	if err != nil {
		t.Fatal(err)
	}
	if tok == "" {
		t.Fatal("token 不应为空")
	}

	st, ok := s.get(tok)
	if !ok || st.Phone != "+8613800000000" || st.PhoneCodeHash != "hash1" {
		t.Fatalf("get 异常: %+v ok=%v", st, ok)
	}
	if st.NeedsPassword {
		t.Fatal("初始不应需要 2FA")
	}

	// update:转入 2FA,刷新 TTL
	if !s.update(tok, func(ls *loginStep) { ls.NeedsPassword = true }) {
		t.Fatal("update 应成功")
	}
	st, _ = s.get(tok)
	if !st.NeedsPassword {
		t.Fatal("update 后应需要 2FA")
	}

	// 过期后取不到
	now = now.Add(6 * time.Minute)
	if _, ok := s.get(tok); ok {
		t.Fatal("过期后不应取到")
	}
	// 过期项 update 失败
	if s.update(tok, func(*loginStep) {}) {
		t.Fatal("过期项 update 应失败")
	}
}

func TestStepStoreDeleteAndSweep(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := newStepStore(time.Minute, func() time.Time { return now })

	tok1, _ := s.create(&loginStep{Phone: "a"})
	tok2, _ := s.create(&loginStep{Phone: "b"})

	s.delete(tok1)
	if _, ok := s.get(tok1); ok {
		t.Fatal("delete 后不应取到")
	}

	now = now.Add(2 * time.Minute) // tok2 过期
	s.sweep()
	if len(s.items) != 0 {
		t.Fatalf("sweep 后应清空,剩 %d", len(s.items))
	}
	_ = tok2
}

func TestGateRespectsConcurrency(t *testing.T) {
	g := newGate(1000, 1000, 2) // 高速率,只测并发上限=2
	ctx := context.Background()

	var (
		concurrent   int
		maxSeen      int
		releaseCh    = make(chan struct{})
		startedCount = make(chan struct{}, 3)
	)
	mu := make(chan struct{}, 1)
	mu <- struct{}{} // 充当互斥锁

	run := func() {
		_ = g.do(ctx, func() error {
			<-mu
			concurrent++
			if concurrent > maxSeen {
				maxSeen = concurrent
			}
			mu <- struct{}{}
			startedCount <- struct{}{}
			<-releaseCh // 阻塞,占住并发槽
			<-mu
			concurrent--
			mu <- struct{}{}
			return nil
		})
	}

	for i := 0; i < 3; i++ {
		go run()
	}
	// 应只有 2 个能同时进入
	<-startedCount
	<-startedCount
	select {
	case <-startedCount:
		t.Fatal("并发上限应为 2,但有第 3 个同时进入")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseCh) // 放行,第三个随后进入
	<-startedCount

	if maxSeen != 2 {
		t.Fatalf("最大并发应为 2,实际 %d", maxSeen)
	}
}

func TestGateContextCancel(t *testing.T) {
	g := newGate(0.0001, 1, 1) // 极低速率,Wait 会阻塞
	// 先耗掉初始令牌
	_ = g.do(context.Background(), func() error { return nil })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	called := false
	err := g.do(ctx, func() error { called = true; return nil })
	if err == nil {
		t.Fatal("ctx 超时应返回错误")
	}
	if called {
		t.Fatal("限速等待被取消时不应执行 f")
	}
}
