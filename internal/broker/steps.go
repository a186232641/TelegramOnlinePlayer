package broker

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// loginStep 是一次登录引导中、跨多个 HTTP 请求保留的短期上下文。
// 前端只持有不透明的 step token,不接触 phone_code_hash 等内部字段(见 §14.1)。
type loginStep struct {
	Phone         string
	PhoneCodeHash string
	NeedsPassword bool // SignIn 返回需要 2FA 后置位
	expiresAt     time.Time
}

// stepStore 以 TTL 管理登录步骤上下文。并发安全;时钟可注入以便测试。
type stepStore struct {
	mu    sync.Mutex
	items map[string]*loginStep
	ttl   time.Duration
	now   func() time.Time
}

func newStepStore(ttl time.Duration, now func() time.Time) *stepStore {
	if now == nil {
		now = time.Now
	}
	return &stepStore{items: map[string]*loginStep{}, ttl: ttl, now: now}
}

// create 生成一个新的 step token 并存入上下文,返回 token。
func (s *stepStore) create(step *loginStep) (string, error) {
	tok, err := newStepToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	step.expiresAt = s.now().Add(s.ttl)
	s.items[tok] = step
	return tok, nil
}

// get 返回未过期的步骤上下文;不存在或已过期返回 (nil,false)。
func (s *stepStore) get(tok string) (*loginStep, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.items[tok]
	if !ok {
		return nil, false
	}
	if s.now().After(st.expiresAt) {
		delete(s.items, tok)
		return nil, false
	}
	return st, true
}

// update 在保留同一 token 的前提下刷新上下文与 TTL(如 SignIn 后转入 2FA)。
func (s *stepStore) update(tok string, mutate func(*loginStep)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.items[tok]
	if !ok || s.now().After(st.expiresAt) {
		delete(s.items, tok)
		return false
	}
	mutate(st)
	st.expiresAt = s.now().Add(s.ttl)
	return true
}

func (s *stepStore) delete(tok string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, tok)
}

// sweep 清理已过期项,供周期性调用。
func (s *stepStore) sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for tok, st := range s.items {
		if now.After(st.expiresAt) {
			delete(s.items, tok)
		}
	}
}

func newStepToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
