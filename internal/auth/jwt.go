// Package auth — токен-автентифікація для gRPC (слайд 32) та RBAC за ролями.
//
// УВАГА: JWT тут реалізовано мінімально (HS256, лише stdlib) — щоб демо не мало
// зовнішніх залежностей і було видно, як це працює. У продакшені користуйтесь
// перевіреною бібліотекою (напр. github.com/golang-jwt/jwt/v5).
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	RoleReader    = "reader"    // читач: може лише дивитися
	RoleLibrarian = "librarian" // бібліотекар: може змінювати інвентар

	// DefaultDemoSecret — ЛИШЕ для локального демо. Не використовуйте в реальних системах.
	DefaultDemoSecret = "libratrack-demo-secret-change-me"
)

var (
	ErrMalformed = errors.New("malformed token")
	ErrSignature = errors.New("bad signature")
	ErrExpired   = errors.New("token expired")
)

type Claims struct {
	Subject   string `json:"sub"`
	Role      string `json:"role"`
	ExpiresAt int64  `json:"exp"` // unix seconds
}

var b64 = base64.RawURLEncoding

// Sign створює підписаний токен HS256.
func Sign(secret []byte, c Claims) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	signingInput := b64.EncodeToString(header) + "." + b64.EncodeToString(payload)
	return signingInput + "." + b64.EncodeToString(mac(secret, signingInput)), nil
}

// Verify перевіряє підпис, алгоритм і строк дії.
func Verify(secret []byte, token string, now time.Time) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrMalformed
	}
	rawHeader, err := b64.DecodeString(parts[0])
	if err != nil {
		return nil, ErrMalformed
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(rawHeader, &header); err != nil {
		return nil, ErrMalformed
	}
	// Жорстко вимагаємо HS256: відсікає атаки з alg=none та підміною алгоритму.
	if header.Alg != "HS256" {
		return nil, ErrMalformed
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil {
		return nil, ErrMalformed
	}
	if !hmac.Equal(sig, mac(secret, parts[0]+"."+parts[1])) { // постійний час
		return nil, ErrSignature
	}
	rawPayload, err := b64.DecodeString(parts[1])
	if err != nil {
		return nil, ErrMalformed
	}
	var c Claims
	if err := json.Unmarshal(rawPayload, &c); err != nil {
		return nil, ErrMalformed
	}
	if c.ExpiresAt == 0 || now.Unix() >= c.ExpiresAt {
		return nil, ErrExpired
	}
	return &c, nil
}

func mac(secret []byte, data string) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(data))
	return h.Sum(nil)
}
