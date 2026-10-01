package adminauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/secretbox"
)

// TOTP 两步验证（RFC 6238：HMAC-SHA1、30 秒、6 位），按管理员自愿启用。
// 密钥用 KEK 信封加密存库（admin_users.totp_secret_enc/totp_dek_wrapped），
// 验证时允许前后各 1 个时间片的时钟偏差，并记录最近一次通过的时间片防止重放。

var (
	ErrTOTPRequired     = errors.New("adminauth: two-factor code required")
	ErrTOTPInvalid      = errors.New("adminauth: invalid two-factor code")
	ErrTOTPUnavailable  = errors.New("adminauth: two-factor authentication requires a server KEK")
	ErrTOTPNotSetUp     = errors.New("adminauth: two-factor authentication has not been set up")
	ErrTOTPAlreadyOn    = errors.New("adminauth: two-factor authentication is already enabled")
	totpEncoding        = base32.StdEncoding.WithPadding(base32.NoPadding)
	totpPeriod          = int64(30)
	totpIssuer          = "uFreeTokens Admin"
	totpAllowedSkewStep = int64(1)
)

// totpCode 计算某个时间片的 6 位验证码。
func totpCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := (binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff) % 1_000_000
	return fmt.Sprintf("%06d", v)
}

// verifyTOTP 返回匹配的时间片；不匹配返回 0。
func verifyTOTP(secret []byte, code string, now time.Time) int64 {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0
	}
	step := now.Unix() / totpPeriod
	for d := -totpAllowedSkewStep; d <= totpAllowedSkewStep; d++ {
		if hmac.Equal([]byte(totpCode(secret, step+d)), []byte(code)) {
			return step + d
		}
	}
	return 0
}

// TOTPSetup 是开始绑定时返回给管理员的信息（只显示这一次）。
type TOTPSetup struct {
	Secret     string `json:"secret"`
	OTPAuthURL string `json:"otpauth_url"`
}

func (s *Service) loadTOTP(ctx context.Context, adminID int64) (secret []byte, enabled bool, lastStep *int64, err error) {
	var enc, dek []byte
	if err := s.db(ctx).QueryRow(ctx,
		`SELECT totp_secret_enc, totp_dek_wrapped, totp_enabled, totp_last_step FROM admin_users WHERE id = $1`, adminID,
	).Scan(&enc, &dek, &enabled, &lastStep); err != nil {
		return nil, false, nil, fmt.Errorf("adminauth: load totp: %w", err)
	}
	if enc == nil {
		return nil, enabled, lastStep, nil
	}
	if s.cfg.Box == nil {
		return nil, enabled, lastStep, ErrTOTPUnavailable
	}
	plain, err := s.cfg.Box.Open(&secretbox.Sealed{Ciphertext: enc, WrappedDEK: dek})
	if err != nil {
		return nil, enabled, lastStep, fmt.Errorf("adminauth: decrypt totp secret: %w", err)
	}
	secret, err = totpEncoding.DecodeString(plain)
	return secret, enabled, lastStep, err
}

// checkTOTP 校验验证码并记录时间片（同一时间片的码不能再用第二次）。
func (s *Service) checkTOTP(ctx context.Context, adminID int64, code string) error {
	secret, _, lastStep, err := s.loadTOTP(ctx, adminID)
	if err != nil {
		return err
	}
	if secret == nil {
		return ErrTOTPNotSetUp
	}
	step := verifyTOTP(secret, code, s.now())
	if step == 0 || (lastStep != nil && step <= *lastStep) {
		return ErrTOTPInvalid
	}
	_, err = s.db(ctx).Exec(ctx, `UPDATE admin_users SET totp_last_step = $2 WHERE id = $1`, adminID, step)
	return err
}

// SetupTOTP 生成新密钥并保存（尚未启用）；已启用时需先停用。
func (s *Service) SetupTOTP(ctx context.Context, adminID int64, email string) (*TOTPSetup, error) {
	if s.cfg.Box == nil {
		return nil, ErrTOTPUnavailable
	}
	_, enabled, _, err := s.loadTOTP(ctx, adminID)
	if err != nil && !errors.Is(err, ErrTOTPUnavailable) {
		return nil, err
	}
	if enabled {
		return nil, ErrTOTPAlreadyOn
	}
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	secret := totpEncoding.EncodeToString(raw)
	sealed, err := s.cfg.Box.Seal(secret)
	if err != nil {
		return nil, fmt.Errorf("adminauth: encrypt totp secret: %w", err)
	}
	if _, err := s.db(ctx).Exec(ctx,
		`UPDATE admin_users SET totp_secret_enc = $2, totp_dek_wrapped = $3, totp_enabled = false, totp_last_step = NULL, updated_at = now() WHERE id = $1`,
		adminID, sealed.Ciphertext, sealed.WrappedDEK); err != nil {
		return nil, fmt.Errorf("adminauth: store totp secret: %w", err)
	}
	label := url.PathEscape(totpIssuer + ":" + email)
	q := url.Values{"secret": {secret}, "issuer": {totpIssuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return &TOTPSetup{Secret: secret, OTPAuthURL: "otpauth://totp/" + label + "?" + q.Encode()}, nil
}

// EnableTOTP 用一个有效验证码确认绑定并启用。
func (s *Service) EnableTOTP(ctx context.Context, adminID int64, code string) error {
	if err := s.checkTOTP(ctx, adminID, code); err != nil {
		return err
	}
	_, err := s.db(ctx).Exec(ctx, `UPDATE admin_users SET totp_enabled = true, updated_at = now() WHERE id = $1`, adminID)
	return err
}

// DisableTOTP 本人停用两步验证（需要当前验证码）。
func (s *Service) DisableTOTP(ctx context.Context, adminID int64, code string) error {
	if err := s.checkTOTP(ctx, adminID, code); err != nil {
		return err
	}
	return s.clearTOTP(ctx, adminID)
}

func (s *Service) clearTOTP(ctx context.Context, adminID int64) error {
	_, err := s.db(ctx).Exec(ctx,
		`UPDATE admin_users SET totp_secret_enc = NULL, totp_dek_wrapped = NULL, totp_enabled = false, totp_last_step = NULL, updated_at = now() WHERE id = $1`, adminID)
	return err
}
