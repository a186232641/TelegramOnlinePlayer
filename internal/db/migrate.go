package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations 读取并按版本号排序嵌入的迁移文件。
// 文件命名约定:`<version>_<desc>.sql`,version 为前导整数。
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, err
	}

	var ms []migration
	seen := map[int]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		idx := strings.IndexByte(e.Name(), '_')
		if idx <= 0 {
			return nil, fmt.Errorf("迁移文件名缺少版本前缀: %s", e.Name())
		}
		ver, err := strconv.Atoi(e.Name()[:idx])
		if err != nil {
			return nil, fmt.Errorf("迁移文件版本前缀非整数: %s", e.Name())
		}
		if prev, ok := seen[ver]; ok {
			return nil, fmt.Errorf("迁移版本号重复: %d (%s 与 %s)", ver, prev, e.Name())
		}
		seen[ver] = e.Name()

		content, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{version: ver, name: e.Name(), sql: string(content)})
	}

	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	return ms, nil
}

// Migrate 顺序应用尚未执行的迁移。每个迁移在独立事务内执行,
// 与 schema_migrations 写入同事务,保证「要么整体生效、要么完全回滚」。
func (p *Pool) Migrate(ctx context.Context, logger *slog.Logger) error {
	if _, err := p.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INT  PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("创建 schema_migrations 失败: %w", err)
	}

	var current int
	if err := p.QueryRow(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("读取当前迁移版本失败: %w", err)
	}

	ms, err := loadMigrations()
	if err != nil {
		return err
	}

	applied := 0
	for _, m := range ms {
		if m.version <= current {
			continue
		}
		if err := p.applyOne(ctx, m); err != nil {
			return fmt.Errorf("应用迁移 %s 失败: %w", m.name, err)
		}
		if logger != nil {
			logger.Info("已应用数据库迁移", "version", m.version, "name", m.name)
		}
		applied++
	}

	if logger != nil {
		logger.Info("数据库迁移完成", "from", current, "applied", applied)
	}
	return nil
}

func (p *Pool) applyOne(ctx context.Context, m migration) error {
	tx, err := p.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // 提交成功后回滚是空操作

	if _, err := tx.Exec(ctx, m.sql); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`,
		m.version, m.name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
