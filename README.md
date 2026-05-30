# Telegram Online Player

主播录播归档系统(个人自用)。设计文档见 [design.md](./design.md)。

## 当前进度

- [x] **Phase 1 · 鉴权基础设施**:配置加载、HMAC cookie session、登录限流、登录/登出/whoami、最小登录页、`hash-password` CLI
- [x] **Phase 2 · PostgreSQL 与目录数据模型**:`channels`/`streamers`/`streamer_alias`/`telegram_media` 表与索引、嵌入式版本化迁移器(启动自动迁移)、catalog 存取层(主播网格 / 时间线 / 按 token 取 / 频道 / 增量 offset / UpsertMedia)、`/healthz` 带 DB ping
- [ ] Phase 3 · 同步服务(tdl 扫描 + 文件名解析)
- [ ] Phase 4 · 缓存播放(下载、归一化、签名 URL、LRU)
- [ ] Phase 5 · 前端主播网格 / 时间线 / 播放页
- [ ] Phase 6 · tdl Web 引导登录

## 本地开发

```sh
# 1. 生成密码哈希(交互式)
go run ./cmd/app hash-password
# 输出形如:$2a$12$xxxx...

# 2. 准备密钥(任选其一)
#    hex 编码 32 字节随机:
#    openssl rand -hex 32
#    或直接给 32+ 字节明文

# 3. 启动服务(PowerShell 示例)
$env:ACCESS_PASSWORD_HASH = '$2a$12$xxxx...'
$env:SESSION_SECRET       = '64-char-hex'
$env:PLAY_URL_SECRET      = '另一个 64-char-hex'
$env:COOKIE_SECURE        = 'false'   # 本地 HTTP 调试关掉
$env:HTTP_ADDR            = ':8080'
# 可选:配置 PostgreSQL 后启用目录功能;为空则降级为 auth-only,启动时仅打印告警
$env:POSTGRES_DSN         = 'postgres://user:pass@localhost:5432/recordings?sslmode=disable'
go run ./cmd/app

# 4. 浏览器打开 http://localhost:8080,输入密码即可登录
```

启动时若配置了 `POSTGRES_DSN`,会自动连接并应用 `internal/db/migrations/` 下的版本化迁移
(版本记录于 `schema_migrations` 表,每个迁移在独立事务内执行)。`GET /healthz` 会附带 DB ping。

## 运行测试

```sh
go test ./...

# catalog 存取层为集成测试,需一个可写的 PostgreSQL,设置后才会运行(否则自动跳过):
$env:TEST_POSTGRES_DSN = 'postgres://user:pass@localhost:5432/test?sslmode=disable'
go test ./internal/catalog/ -run Store -v
```

## 目录结构

```
cmd/app/              主程序(serve / hash-password 两个子命令)
internal/config/      环境变量加载
internal/auth/        session、play URL 签名、登录限流
internal/db/          PostgreSQL 连接池 + 嵌入式版本化迁移器
internal/db/migrations/   SQL 迁移文件(<version>_<desc>.sql)
internal/catalog/     目录领域模型与存取层(Store)
internal/httpserver/  HTTP 路由与处理器
internal/httpserver/web/  静态前端资源(嵌入 binary)
```
