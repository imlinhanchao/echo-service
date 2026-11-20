package secret

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func newKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := newKey(t)
	plain := []byte(`{"domain":"https://z-lib.gd","remix_userkey":"FAKE-KEY"}`)
	enc, err := Encrypt(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(enc, plain) {
		t.Fatal("密文不应等于明文")
	}
	got, err := Decrypt(key, enc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("解密结果不一致: %s", got)
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	key := newKey(t)
	enc, err := Encrypt(key, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(newKey(t), enc); err == nil {
		t.Fatal("错误密钥不应解密成功")
	}
}

func TestJSONHelpers(t *testing.T) {
	key := newKey(t)
	type creds struct {
		Domain string `json:"domain"`
		Key    string `json:"key"`
	}
	in := creds{Domain: "https://z-lib.gd", Key: "FAKE-KEY"}
	enc, err := EncryptJSON(key, in)
	if err != nil {
		t.Fatal(err)
	}
	var out creds
	if err := DecryptJSON(key, enc, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("往返结果不一致: %+v", out)
	}
}
