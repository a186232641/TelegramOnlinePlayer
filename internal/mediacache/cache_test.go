package mediacache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTemp(t *testing.T, c *Cache, name string, size int) string {
	t.Helper()
	p := c.TempPath(name)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newCache(t *testing.T, max int64, ttl time.Duration) (*Cache, *time.Time) {
	t.Helper()
	c, err := New(t.TempDir(), max, ttl)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	clk := &now
	c.now = func() time.Time { return *clk }
	return c, clk
}

func TestStoreAndLookup(t *testing.T) {
	c, _ := newCache(t, 1000, time.Minute)
	tmp := writeTemp(t, c, "a.part", 100)
	final, err := c.Store("keyA", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(final); err != nil {
		t.Fatalf("正式文件应存在: %v", err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatal("临时文件应已被 rename 移走")
	}
	if c.TotalBytes() != 100 {
		t.Fatalf("total=%d want 100", c.TotalBytes())
	}
	got, ok := c.Lookup("keyA")
	if !ok || got != final {
		t.Fatalf("Lookup 失败: %q ok=%v", got, ok)
	}
	if _, ok := c.Lookup("missing"); ok {
		t.Fatal("不存在的 key 不应命中")
	}
}

func TestRejectTooLarge(t *testing.T) {
	c, _ := newCache(t, 50, time.Minute)
	tmp := writeTemp(t, c, "big.part", 100)
	if _, err := c.Store("big", tmp); err != ErrTooLarge {
		t.Fatalf("应 ErrTooLarge,得 %v", err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatal("超大临时文件应被清理")
	}
}

func TestLRUEviction(t *testing.T) {
	c, clk := newCache(t, 250, time.Minute)

	// 放 A、B、C 各 100(总 300 > 250 需淘汰)。让访问时间拉开,且都超出 TTL 窗口。
	_, _ = c.Store("A", writeTemp(t, c, "a", 100))
	*clk = clk.Add(2 * time.Minute) // A 进入 TTL 窗口外
	_, _ = c.Store("B", writeTemp(t, c, "b", 100))
	*clk = clk.Add(2 * time.Minute)

	// 访问 A,使其成为最近访问(B 变成最久未访问)。
	if _, ok := c.Lookup("A"); !ok {
		t.Fatal("A 应在")
	}
	*clk = clk.Add(2 * time.Minute) // 让 B 也超出 TTL 窗口

	// 放 C:总会到 300,需淘汰约 1 个。最久未访问的是 B,应被淘汰。
	if _, err := c.Store("C", writeTemp(t, c, "c", 100)); err != nil {
		t.Fatalf("Store C: %v", err)
	}
	if _, ok := c.Lookup("B"); ok {
		t.Fatal("B 应被 LRU 淘汰")
	}
	if _, ok := c.Lookup("A"); !ok {
		t.Fatal("A 最近访问过,不应被淘汰")
	}
	if _, ok := c.Lookup("C"); !ok {
		t.Fatal("C 应在")
	}
}

func TestTTLProtectsRecent(t *testing.T) {
	c, _ := newCache(t, 150, time.Minute)
	_, _ = c.Store("A", writeTemp(t, c, "a", 100)) // 刚访问,受 TTL 保护
	// 放 B(100)→ 总会 200>150,需淘汰;但 A 在 TTL 窗口内受保护 → 无可淘汰 → ErrNoSpace
	if _, err := c.Store("B", writeTemp(t, c, "b", 100)); err != ErrNoSpace {
		t.Fatalf("受保护项占满应 ErrNoSpace,得 %v", err)
	}
}

func TestTmpCleanedOnNew(t *testing.T) {
	dir := t.TempDir()
	tmpDir := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	leftover := filepath.Join(tmpDir, "stale.part")
	_ = os.WriteFile(leftover, []byte("x"), 0o644)
	if _, err := New(dir, 1000, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatal("New 应清理临时区遗留文件")
	}
}
