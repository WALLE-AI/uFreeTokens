package console

import "testing"

func TestHashPassword_VerifyPassword_RoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	ok, err := VerifyPassword("correct horse battery staple", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("VerifyPassword() = false, want true for the correct password")
	}
}

func TestVerifyPassword_WrongPasswordFails(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	ok, err := VerifyPassword("wrong password", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if ok {
		t.Error("VerifyPassword() = true, want false for a wrong password")
	}
}

func TestHashPassword_ProducesUniqueSaltsPerCall(t *testing.T) {
	h1, err := HashPassword("same password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	h2, err := HashPassword("same password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if h1 == h2 {
		t.Error("HashPassword() produced identical output for two calls with the same password (salt not randomized?)")
	}
}

func TestVerifyPassword_RejectsMalformedHash(t *testing.T) {
	cases := []string{
		"",
		"not-a-phc-string",
		"$argon2id$v=19$m=65536,t=2,p=2$onlyfourfields",
		"$bcrypt$v=19$m=65536,t=2,p=2$c2FsdA$aGFzaA",
	}
	for _, c := range cases {
		if _, err := VerifyPassword("anything", c); err == nil {
			t.Errorf("VerifyPassword(_, %q) returned nil error, want a format error", c)
		}
	}
}

func TestVerifyDummyPassword_NeverPanics(t *testing.T) {
	// VerifyDummyPassword 的返回值本来就没有导出给调用方判断，这里只验证它不会
	// panic——Login 在"邮箱不存在"分支依赖它安全地跑完一次完整的 argon2id 计算。
	VerifyDummyPassword("whatever the attacker typed")
}
