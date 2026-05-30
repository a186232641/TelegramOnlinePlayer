
-- Phase 2 · 目录数据模型(对应 design.md §5)
-- 注意:Postgres 会把未加引号的标识符折叠为小写,Go 侧查询统一用小写列名。

-- 频道配置:每个频道对应一年(或一组)录播
CREATE TABLE channels (
    channel_id BIGINT  PRIMARY KEY,            -- Telegram 频道 id
    label      TEXT    NOT NULL,               -- 比如 "2024"
    enabled    BOOLEAN NOT NULL DEFAULT true
);

-- 主播规范化:处理同一主播跨频道命名不一致(首期可不写入数据,StreamerId 恒 NULL)
-- 须先于 telegram_media 创建,后者外键引用本表。
CREATE TABLE streamers (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    display_name TEXT NOT NULL,
    avatar       TEXT
);

CREATE TABLE streamer_alias (
    alias       TEXT   PRIMARY KEY,            -- 文件名里出现的原始主播名
    streamer_id BIGINT NOT NULL REFERENCES streamers(id)
);

-- 录播条目:目录与播放的核心表
CREATE TABLE telegram_media (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    channel_id    BIGINT      NOT NULL REFERENCES channels(channel_id),
    message_id    BIGINT      NOT NULL,
    file_name     TEXT        NOT NULL,                 -- 原始 Telegram 文件名(带冒号)
    file_size     BIGINT      NOT NULL,
    mime_type     TEXT,
    duration_sec  INT,
    streamer      TEXT,                                 -- 解析得出的原始名
    streamer_id   BIGINT      REFERENCES streamers(id), -- 规范化后回填;未启用别名表时为 NULL
    recorded_at   TIMESTAMPTZ,                          -- 解析得出,排序/展示用
    uploaded_at   TIMESTAMPTZ NOT NULL,                 -- 消息上传时间,用于增量 diff
    stream_token  TEXT        NOT NULL UNIQUE,          -- 资源稳定 ID;密码学随机;播放需登录后换签名 URL

    -- 探测结果(同步期写入,决定播放路径)
    container     TEXT,                                 -- mp4 / flv / ts
    video_codec   TEXT,                                 -- h264 / hevc / ...
    audio_codec   TEXT,                                 -- aac / ...
    faststart     BOOLEAN,                              -- mp4 的 moov 是否在前
    play_mode     TEXT,                                 -- passthrough / remux / transcode

    -- 缓存/媒体状态(与目录状态正交)
    cache_state   TEXT        NOT NULL DEFAULT 'none',  -- none/preparing/ready/failed(passthrough 恒为 none)
    cache_path    TEXT,                                 -- 归一化产物落盘路径
    last_error    TEXT,                                 -- 下载/转码失败原因

    thumb_path    TEXT,
    status        TEXT        NOT NULL DEFAULT 'pending', -- pending/ready/unparsed/stale/deleted
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (channel_id, message_id)                      -- message_id 仅频道内唯一
);

-- 主查询:某主播的录播按时间排
CREATE INDEX ix_media_streamer_time   ON telegram_media (streamer, recorded_at);
-- 启用规范化后按 streamer_id 查
CREATE INDEX ix_media_streamerid_time ON telegram_media (streamer_id, recorded_at);
