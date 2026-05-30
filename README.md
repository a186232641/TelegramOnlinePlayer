# Telegram Online Player

主播录播归档系统(个人自用)。设计文档见 [design.md](./design.md)。

## 当前进度

- [x] **Phase 1 · 鉴权基础设施**:配置加载、HMAC cookie session、登录限流、登录/登出/whoami、最小登录页、`hash-password` CLI
- [ ] Phase 2 · PostgreSQL 与目录数据模型
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
go run ./cmd/app

# 4. 浏览器打开 http://localhost:8080,输入密码即可登录
```

## 运行测试

```sh
go test ./...
```

## 目录结构

```
cmd/app/              主程序(serve / hash-password 两个子命令)
internal/config/      环境变量加载
internal/auth/        session、play URL 签名、登录限流
internal/httpserver/  HTTP 路由与处理器
internal/httpserver/web/  静态前端资源(嵌入 binary)
```
