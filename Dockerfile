# syntax=docker/dockerfile:1

# ---- 构建阶段:一次编译三个二进制 ----
FROM golang:1.25-alpine AS builder
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-buildvcs=false
# 先拉依赖(利用层缓存)
COPY go.mod go.sum ./
RUN go mod download
# 再拷源码构建(web 静态资源经 go:embed 一并打入二进制)
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/broker ./cmd/broker \
 && go build -trimpath -ldflags="-s -w" -o /out/app    ./cmd/app \
 && go build -trimpath -ldflags="-s -w" -o /out/sync   ./cmd/sync

# ---- tdl-broker:系统对 Telegram 的唯一出口,独占 session 卷 ----
FROM alpine:3.20 AS broker
RUN apk add --no-cache ca-certificates wget \
 && adduser -D -u 10001 app \
 && mkdir -p /data/tdl && chown -R app /data
USER app
COPY --from=builder /out/broker /usr/local/bin/broker
EXPOSE 8090
ENTRYPOINT ["broker"]

# ---- backend:目录 API + 鉴权 + 透传/缓存播放,需要 ffmpeg ----
FROM alpine:3.20 AS backend
RUN apk add --no-cache ca-certificates ffmpeg \
 && adduser -D -u 10001 app \
 && mkdir -p /cache && chown -R app /cache
USER app
COPY --from=builder /out/app /usr/local/bin/app
EXPOSE 8080
ENTRYPOINT ["app"]

# ---- sync:同步服务(cron / 按需触发),经 broker 访问 Telegram ----
FROM alpine:3.20 AS sync
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
USER app
COPY --from=builder /out/sync /usr/local/bin/sync
ENTRYPOINT ["sync"]
