package auth

import "testing"

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("正确密码校验失败")
	}
	if VerifyPassword(hash, "wrong password") {
		t.Fatal("错误密码不应通过")
	}
	if VerifyPassword("not-a-hash", "x") {
		t.Fatal("非法 hash 不应通过")
	}
}

func TestHashIsSalted(t *testing.T) {
	a, _ := HashPassword("same-password")
	b, _ := HashPassword("same-password")
	if a == b {
		t.Fatal("两次 hash 不应相同（salt 失效）")
	}
}

func TestTokens(t *testing.T) {
	tok1, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	tok2, _ := NewToken()
	if tok1 == tok2 {
		t.Fatal("token 不应重复")
	}
	if len(tok1) < 32 {
		t.Fatalf("token 太短: %d", len(tok1))
	}
	if HashToken(tok1) == tok1 {
		t.Fatal("HashToken 不应返回明文")
	}
	if len(HashToken(tok1)) != 64 {
		t.Fatalf("sha256 hex 长度应为 64，得到 %d", len(HashToken(tok1)))
	}
}
