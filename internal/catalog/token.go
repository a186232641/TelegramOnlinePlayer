package catalog

import (
	"crypto/rand"
	"encoding/base64"
)

// streamTokenBytes 是 StreamToken 的随机字节数,16 字节(128 bit)足以防枚举。
const streamTokenBytes = 16

// NewStreamToken 生成一个密码学随机的资源 ID(base64url,无填充)。
// 它仅作为稳定的资源内部 ID(避免暴露 MessageId/物理路径),
// 必须不可预测——否则防枚举形同虚设(见 design §5 说明)。
func NewStreamToken() (string, error) {
	b := make([]byte, streamTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
