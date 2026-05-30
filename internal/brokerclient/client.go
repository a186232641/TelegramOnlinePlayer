// Package brokerclient 是 broker 内部 HTTP API 的 Go 客户端,
// 供 backend/sync 调用(它们不直连 Telegram,一切经 broker,见 design §4.5、§9.3)。
package brokerclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"telegram-online-player/internal/syncer"
)

// Client 通过共享密钥(Bearer)访问 broker 内部 API。
type Client struct {
	base  string
	token string
	http  *http.Client
}

// New 构造客户端。baseURL 形如 http://broker:8090。httpClient 可为 nil(用默认)。
func New(baseURL, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{base: baseURL, token: token, http: httpClient}
}

// 编译期保证客户端可作为同步服务的 Exporter 注入。
var _ syncer.Exporter = (*Client)(nil)

// ---- 数据能力 ----

// ExportHistory 实现 syncer.Exporter。
func (c *Client) ExportHistory(ctx context.Context, channelID, since int64) ([]syncer.ExportedMessage, error) {
	q := url.Values{}
	q.Set("channel_id", strconv.FormatInt(channelID, 10))
	q.Set("since", strconv.FormatInt(since, 10))
	var out []syncer.ExportedMessage
	if err := c.getJSON(ctx, "/tg/export?"+q.Encode(), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// FileSize 返回某消息文档大小。
func (c *Client) FileSize(ctx context.Context, channelID, messageID int64) (int64, error) {
	q := url.Values{}
	q.Set("channel_id", strconv.FormatInt(channelID, 10))
	q.Set("message_id", strconv.FormatInt(messageID, 10))
	var out struct {
		Size int64 `json:"size"`
	}
	if err := c.getJSON(ctx, "/tg/file-size?"+q.Encode(), &out); err != nil {
		return 0, err
	}
	return out.Size, nil
}

// Download 把整文件流式写入 w。
func (c *Client) Download(ctx context.Context, channelID, messageID int64, w io.Writer) error {
	q := url.Values{}
	q.Set("channel_id", strconv.FormatInt(channelID, 10))
	q.Set("message_id", strconv.FormatInt(messageID, 10))
	resp, err := c.do(ctx, http.MethodGet, "/tg/download?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.errFromResp(resp)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// ReadRange 读取 [offset, offset+length) 区间字节。
func (c *Client) ReadRange(ctx context.Context, channelID, messageID, offset, length int64) ([]byte, error) {
	q := url.Values{}
	q.Set("channel_id", strconv.FormatInt(channelID, 10))
	q.Set("message_id", strconv.FormatInt(messageID, 10))
	q.Set("offset", strconv.FormatInt(offset, 10))
	q.Set("length", strconv.FormatInt(length, 10))
	resp, err := c.do(ctx, http.MethodGet, "/tg/range?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.errFromResp(resp)
	}
	return io.ReadAll(resp.Body)
}

// ---- 认证代理(供 backend 的 /admin/tdl-* 转调,见 §14) ----

// Status 返回登录状态。
func (c *Client) Status(ctx context.Context) (loggedIn bool, phone string, err error) {
	var out struct {
		LoggedIn bool   `json:"logged_in"`
		Phone    string `json:"phone"`
	}
	if err = c.getJSON(ctx, "/tg/status", &out); err != nil {
		return false, "", err
	}
	return out.LoggedIn, out.Phone, nil
}

// SendCode 触发验证码,返回步骤 token。
func (c *Client) SendCode(ctx context.Context, phone string) (string, error) {
	var out struct {
		StepToken string `json:"step_token"`
	}
	if err := c.postJSON(ctx, "/tg/send-code", map[string]string{"phone": phone}, &out); err != nil {
		return "", err
	}
	return out.StepToken, nil
}

// SignIn 用验证码登录,返回是否需要 2FA。
func (c *Client) SignIn(ctx context.Context, stepToken, code string) (bool, error) {
	var out struct {
		NeedPassword bool `json:"need_password"`
	}
	if err := c.postJSON(ctx, "/tg/sign-in",
		map[string]string{"step_token": stepToken, "code": code}, &out); err != nil {
		return false, err
	}
	return out.NeedPassword, nil
}

// CheckPassword 用 2FA 密码完成登录。
func (c *Client) CheckPassword(ctx context.Context, stepToken, password string) error {
	return c.postJSON(ctx, "/tg/check-password",
		map[string]string{"step_token": stepToken, "password": password}, nil)
}

// Logout 注销并清理 session。
func (c *Client) Logout(ctx context.Context) error {
	return c.postJSON(ctx, "/tg/logout", map[string]string{}, nil)
}

// ---- 底层 ----

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.errFromResp(resp)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) postJSON(ctx context.Context, path string, in, out any) error {
	buf, err := json.Marshal(in)
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, http.MethodPost, path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.errFromResp(resp)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// errFromResp 把非 2xx 响应转为带状态码与服务端 error 字段的错误。
func (c *Client) errFromResp(resp *http.Response) error {
	var e struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&e)
	if e.Error != "" {
		return fmt.Errorf("broker %d: %s", resp.StatusCode, e.Error)
	}
	return fmt.Errorf("broker 返回状态 %d", resp.StatusCode)
}
