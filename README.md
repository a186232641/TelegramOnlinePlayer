# Telegram Online Player

主播录播归档系统(个人自用)。设计文档见 [design.md](./design.md)。

## 当前进度

- [x] **Phase 1 · 鉴权基础设施**:配置加载、HMAC cookie session、登录限流、登录/登出/whoami、最小登录页、`hash-password` CLI
- [x] **Phase 2 · PostgreSQL 与目录数据模型**:`channels`/`streamers`/`streamer_alias`/`telegram_media` 表与索引、嵌入式版本化迁移器(启动自动迁移)、catalog 存取层(主播网格 / 时间线 / 按 token 取 / 频道 / 增量 offset / UpsertMedia)、`/healthz` 带 DB ping
- [x] **Phase 3 · 同步服务**:文件名解析器(§6,锚定时间戳/钉定时区/失败标 unparsed)、`StreamToken` 生成器、`Exporter` 接口 + `Syncer` 编排(遍历启用频道 / 增量 offset / 解析 / 探测 PlayMode / UpsertMedia / 设 Status);`cmd/sync`(run / add-channel / list-channels)经 `brokerclient` 接 broker;PlayMode 探测(§4.1.5 轻量分类:mp4→passthrough、flv/ts→remux、其余→transcode)。
  - **tdl-broker(`cmd/broker`)**:基于 gotd 的 MTProto 单一出口。生命周期 + session 文件存储、登录流程(发码/验证码/2FA/状态/注销,步骤 token + TTL)、令牌桶+并发门限速、历史导出 / 整文件下载 / 1MB 对齐 Range 读、内部 HTTP API(共享密钥 Bearer)、`brokerclient`(实现 `syncer.Exporter` / `MediaSource` / `AdminBroker`)。
  - **待办(细化)**:字节级 faststart/编码探测(broker 拉文件头精确判定 passthrough vs remux)、缩略图(§4.1.6)、全量对账检删(§4.1.3)
- [x] **目录 API**:`/api/streamers`(主播网格)、`/api/timeline?streamer=`(跨年时间线)、`/api/media/{token}`(详情),均需登录 cookie;响应 DTO 不泄露 `cache_path`/`last_error` 等内部字段;未配置 DB 时回 503。以 fake store 单测。
- [x] **Phase 4 · 缓存播放**:签名播放 URL(换签 200/202 + `/status` 轮询)、`/play/{token}` 校验签名后三路分流(passthrough 经 broker Range 透传 206 / 缓存命中 `ServeFile` / 未就绪 409);`mediacache` 容量上限 + LRU + TTL 淘汰保护 + 原子写 + 超大拒绝;`normalize` ffprobe/ffmpeg remux+transcode(参数数组防注入、+faststart);`mediaprep` 异步编排(下载→归一化→原子入缓存→更新状态)+ single-flight + 转码 worker 池。均以 fake 单测;真转码需本机 ffmpeg、透传/下载需 broker 已登录。
- [x] **Phase 5 · 前端(主播网格 / 时间线 / 播放页)**:纯 JS SPA(hash 路由,无构建步骤,`go:embed` 进 binary)。`whoami` 鉴权门 + 登录视图;主播网格 → 时间线 → 播放页三级;播放页按 §13.4 换签契约编写(`/play-url` → ready/202 轮询 `/status`),Phase 4 接口缺位时优雅降级提示。受保护接口 401 自动回登录。静态资源嵌入有 Go 测试守卫。
- [x] **Phase 6 · tdl Web 引导登录**:backend `/admin/tdl-*`(登录 cookie 保护)转调 broker `/tg/*`——状态 / 发码 / 验证码 / 2FA / 注销;`AdminBroker` 接口 + main 接线;前端 `#/admin` 登录向导(手机号→验证码→2FA)+ 顶栏入口。以 fake 单测。

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
# 可选:文件名时间戳的假定时区(见 design §6),默认 Asia/Shanghai;tzdata 已内嵌
$env:MEDIA_TIMEZONE       = 'Asia/Shanghai'
# 可选:接入 broker 后启用在线播放(透传 + remux/transcode 缓存)
$env:BROKER_URL           = 'http://localhost:8090'
$env:BROKER_INTERNAL_TOKEN= '与 broker 一致的共享密钥'
$env:CACHE_DIR            = './cache'
$env:CACHE_MAX_BYTES      = '21474836480'  # 20 GiB,含下载/转码临时区
$env:TRANSCODE_CONCURRENCY= '1'            # ffmpeg 转码并发上限
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
cmd/app/              backend 主程序(serve / hash-password 两个子命令)
cmd/broker/           tdl-broker 主程序(独占 MTProto session 的 Telegram 出口)
cmd/sync/             同步服务入口(run / add-channel / list-channels)
internal/config/      环境变量加载
internal/auth/        session、play URL 签名、登录限流
internal/db/          PostgreSQL 连接池 + 嵌入式版本化迁移器
internal/db/migrations/   SQL 迁移文件(<version>_<desc>.sql)
internal/catalog/     目录领域模型与存取层(Store)、StreamToken 生成
internal/syncer/      同步服务:文件名解析(§6)+ Exporter 接口 + Syncer 编排
internal/broker/      gotd MTProto 出口:生命周期/登录/限速/导出/下载/Range + 内部 HTTP API
internal/brokerclient/    broker 内部 API 的 Go 客户端(实现 syncer.Exporter / MediaSource)
internal/mediacache/  归一化产物缓存:容量上限 + LRU + TTL 保护 + 原子写
internal/normalize/   ffprobe/ffmpeg 探测与 remux/transcode 归一化
internal/mediaprep/   remux/transcode 冷路径异步编排(下载→归一化→入缓存,single-flight)
internal/httpserver/  backend HTTP 路由与处理器
internal/httpserver/web/  静态前端资源(嵌入 binary):index.html 壳 + app.js(SPA)+ app.css
```

## tdl-broker(Telegram 出口)

broker 是系统对 Telegram 的**唯一出口**,独占一份 MTProto session。backend/sync 经 `brokerclient`
调用它,自身不碰 session(见 design §4.5、§11、§14)。本地运行示例:

```sh
$env:TG_API_ID            = '123456'              # my.telegram.org 申请
$env:TG_API_HASH          = 'xxxxxxxx'
$env:BROKER_INTERNAL_TOKEN= '与 backend 共享的随机密钥'
$env:TG_SESSION_PATH      = './data/tdl/session.json'  # 0700 目录,仅 broker 可见
$env:BROKER_ADDR          = ':8090'
go run ./cmd/broker
# 首次需经后台引导登录(Phase 6 的 /admin/tdl-login,内部转调 broker 的 /tg/send-code 等)
```

| broker 环境变量 | 含义 |
| --- | --- |
| `TG_API_ID` / `TG_API_HASH` | MTProto 接入凭据(必填) |
| `BROKER_INTERNAL_TOKEN` | backend↔broker 共享密钥,Bearer 鉴权(必填) |
| `TG_SESSION_PATH` | session 文件路径(默认 `./data/tdl/session.json`) |
| `BROKER_ADDR` | 内部 HTTP API 监听地址(默认 `:8090`) |
| `BROKER_RPS` / `BROKER_BURST` / `BROKER_MAX_CONCURRENT` | 对 Telegram 的限速与并发上限 |
| `BROKER_LOGIN_STEP_TTL` | 登录步骤上下文存活时间(默认 5m) |

## 端到端跑通顺序

```sh
# 1) 起 broker(见上),在网页 → 顶栏「Telegram 登录」完成首次登录(或直接调 /admin/tdl-*)
# 2) 配置频道并同步(sync 直接读 POSTGRES_DSN / BROKER_URL / BROKER_INTERNAL_TOKEN)
go run ./cmd/sync add-channel -1001234567890 2024
go run ./cmd/sync list-channels
go run ./cmd/sync run            # 经 broker 导出→解析→探测 PlayMode→入库
# 3) 浏览器打开 backend,登录后即可浏览主播网格 / 时间线 / 播放
```
