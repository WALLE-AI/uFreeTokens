package adminauth

import (
	"testing"
	"time"
)

// RFC 6238 附录 B 的 SHA1 测试向量（取 8 位结果的后 6 位）。
func TestTOTPCode_RFC6238Vectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	for unix, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		if got := totpCode(secret, unix/30); got != want {
			t.Errorf("totpCode(t=%d) = %s, want %s", unix, got, want)
		}
	}
}

func TestVerifyTOTP_AllowsOneStepOfSkew(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1234567890, 0)
	step := now.Unix() / 30
	if verifyTOTP(secret, totpCode(secret, step-1), now) != step-1 {
		t.Error("previous step should be accepted")
	}
	if verifyTOTP(secret, totpCode(secret, step+2), now) != 0 {
		t.Error("code two steps ahead must be rejected")
	}
	if verifyTOTP(secret, "12345", now) != 0 {
		t.Error("malformed code must be rejected")
	}
}
