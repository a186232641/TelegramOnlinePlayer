// Package mediacache 是归一化产物(remux/transcode)的本地缓存:
// 总容量上限 + LRU 淘汰 + 原子写 + 淘汰保护 TTL 窗口(对应 design.md §8)。
// passthrough 内容不入缓存,故压力较小。
package mediacache

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var (
	// ErrTooLarge 表示单个文件超过缓存总上限,无法缓存(应靠 passthrough 规避,见 §8)。
	ErrTooLarge = errors.New("mediacache: 文件超过缓存容量上限")
	// ErrNoSpace 表示腾不出空间(可淘汰项不足,受保护项占满)。
	ErrNoSpace = errors.New("mediacache: 空间不足且无可淘汰项")
)

type entry struct {
	key        string
	size       int64
	lastAccess time.Time
}

// Cache 管理缓存目录的容量与淘汰。并发安全;时钟可注入以便测试。
type Cache struct {
	dir      string
	tmpDir   string
	maxBytes int64
	ttl      time.Duration // 最近访问保护窗口:窗口内访问过的项不淘汰

	mu      sync.Mutex
	entries map[string]*entry
	total   int64
	now     func() time.Time
}

// New 创建缓存,dir 为正式产物目录,其下 tmp/ 为下载/转码临时区(计入同一账本)。
func New(dir string, maxBytes int64, ttl time.Duration) (*Cache, error) {
	tmpDir := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, err
	}
	// 清理上次遗留的临时文件(*.part 等半成品)。
	_ = cleanDir(tmpDir)
	return &Cache{
		dir:      dir,
		tmpDir:   tmpDir,
		maxBytes: maxBytes,
		ttl:      ttl,
		entries:  map[string]*entry{},
		now:      time.Now,
	}, nil
}

// Lookup 返回已缓存文件的路径并刷新其访问时间;未命中返回 ok=false。
func (c *Cache) Lookup(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return "", false
	}
	e.lastAccess = c.now()
	return c.finalPath(key), true
}

// TempPath 返回临时区内的一个路径,供下载/转码写半成品(完成后用 Store 原子入库)。
func (c *Cache) TempPath(name string) string {
	return filepath.Join(c.tmpDir, name)
}

// Store 把临时文件原子地纳入缓存(stat 大小 → 腾空间 → rename → 登记)。
// 返回正式路径。文件大于总上限返回 ErrTooLarge(并删除临时文件)。
func (c *Cache) Store(key, tmpPath string) (string, error) {
	fi, err := os.Stat(tmpPath)
	if err != nil {
		return "", err
	}
	size := fi.Size()

	c.mu.Lock()
	defer c.mu.Unlock()

	if size > c.maxBytes {
		_ = os.Remove(tmpPath)
		return "", ErrTooLarge
	}
	if err := c.evictToFitLocked(size); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}

	final := c.finalPath(key)
	if err := os.Rename(tmpPath, final); err != nil {
		return "", fmt.Errorf("原子入缓存失败: %w", err)
	}
	// 若 key 已存在(重复准备),先扣旧大小。
	if old, ok := c.entries[key]; ok {
		c.total -= old.size
	}
	c.entries[key] = &entry{key: key, size: size, lastAccess: c.now()}
	c.total += size
	return final, nil
}

// evictToFitLocked 按 LRU 淘汰直到能容纳 size;最近 ttl 窗口内访问过的项受保护不淘汰。
func (c *Cache) evictToFitLocked(size int64) error {
	if c.total+size <= c.maxBytes {
		return nil
	}
	// 按最后访问时间升序(最久未访问优先淘汰)。
	es := make([]*entry, 0, len(c.entries))
	for _, e := range c.entries {
		es = append(es, e)
	}
	sort.Slice(es, func(i, j int) bool { return es[i].lastAccess.Before(es[j].lastAccess) })

	now := c.now()
	for _, e := range es {
		if c.total+size <= c.maxBytes {
			break
		}
		if now.Sub(e.lastAccess) <= c.ttl {
			continue // 受保护:可能正在被观看(一次观看有多个独立 Range 请求,见 §8)
		}
		_ = os.Remove(c.finalPath(e.key))
		delete(c.entries, e.key)
		c.total -= e.size
	}
	if c.total+size > c.maxBytes {
		return ErrNoSpace
	}
	return nil
}

// TotalBytes 返回当前已缓存字节数(测试/运维用)。
func (c *Cache) TotalBytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

func (c *Cache) finalPath(key string) string {
	return filepath.Join(c.dir, key+".mp4")
}

func cleanDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
	return nil
}
