package broker

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

// AuthStatus 是当前 Telegram 登录状态(见 §14.2)。
type AuthStatus struct {
	LoggedIn bool
	Phone    string
}

var (
	// ErrStepNotFound 表示步骤 token 不存在或已过期。
	ErrStepNotFound = errors.New("登录步骤不存在或已过期")
	// ErrPasswordNotNeeded 表示当前步骤并不需要 2FA 密码。
	ErrPasswordNotNeeded = errors.New("当前步骤无需 2FA 密码")
)

// Status 查询当前账号登录状态。
func (b *Broker) Status(ctx context.Context) (AuthStatus, error) {
	var st AuthStatus
	err := b.call(ctx, func(ctx context.Context) error {
		s, err := b.client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		st.LoggedIn = s.Authorized
		if s.User != nil {
			st.Phone, _ = s.User.GetPhone()
		}
		return nil
	})
	return st, err
}

// SendCode 触发 Telegram 下发验证码,返回一个步骤 token 供后续 SignIn 使用。
// 前端只持有该 token,不接触 phone_code_hash。
func (b *Broker) SendCode(ctx context.Context, phone string) (string, error) {
	var hash string
	err := b.call(ctx, func(ctx context.Context) error {
		sent, err := b.client.Auth().SendCode(ctx, phone, auth.SendCodeOptions{})
		if err != nil {
			return err
		}
		code, ok := sent.(*tg.AuthSentCode)
		if !ok {
			return fmt.Errorf("非预期的 SendCode 响应类型: %T", sent)
		}
		hash = code.PhoneCodeHash
		return nil
	})
	if err != nil {
		return "", err
	}
	return b.steps.create(&loginStep{Phone: phone, PhoneCodeHash: hash})
}

// SignIn 用验证码完成登录。若账号开启了 2FA,返回 needPassword=true,
// 调用方应据此引导用户输入密码并调用 CheckPassword(沿用同一 step token)。
func (b *Broker) SignIn(ctx context.Context, stepToken, code string) (needPassword bool, err error) {
	step, ok := b.steps.get(stepToken)
	if !ok {
		return false, ErrStepNotFound
	}
	err = b.call(ctx, func(ctx context.Context) error {
		_, signErr := b.client.Auth().SignIn(ctx, step.Phone, code, step.PhoneCodeHash)
		if errors.Is(signErr, auth.ErrPasswordAuthNeeded) {
			needPassword = true
			return nil
		}
		return signErr
	})
	if err != nil {
		return false, err
	}
	if needPassword {
		b.steps.update(stepToken, func(ls *loginStep) { ls.NeedsPassword = true })
		return true, nil
	}
	b.steps.delete(stepToken)
	return false, nil
}

// CheckPassword 用 2FA 密码完成登录(SignIn 返回 needPassword=true 后调用)。
func (b *Broker) CheckPassword(ctx context.Context, stepToken, password string) error {
	step, ok := b.steps.get(stepToken)
	if !ok {
		return ErrStepNotFound
	}
	if !step.NeedsPassword {
		return ErrPasswordNotNeeded
	}
	err := b.call(ctx, func(ctx context.Context) error {
		_, pErr := b.client.Auth().Password(ctx, password)
		return pErr
	})
	if err != nil {
		return err
	}
	b.steps.delete(stepToken)
	return nil
}

// Logout 注销当前账号并清理 session 文件(账号被盗/转移设备场景,见 §14.3)。
func (b *Broker) Logout(ctx context.Context) error {
	if err := b.call(ctx, func(ctx context.Context) error {
		_, err := b.api.AuthLogOut(ctx)
		return err
	}); err != nil {
		return err
	}
	if err := os.Remove(b.cfg.SessionPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除 session 文件失败: %w", err)
	}
	return nil
}
