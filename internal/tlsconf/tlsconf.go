// Package tlsconf — побудова tls.Config для mTLS (слайди 30–31).
//
// Сертифікати для демо створює `go run ./cmd/gencerts`.
package tlsconf

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
)

// ServerConfig: сервер пред'являє свій сертифікат І ВИМАГАЄ сертифікат клієнта,
// підписаний нашим CA. RequireAndVerifyClientCert — це і є "точка примусу":
// без нього сервер прийняв би TLS від будь-якого клієнта (слайд 31).
func ServerConfig(dir string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, "server.pem"), filepath.Join(dir, "server-key.pem"))
	if err != nil {
		return nil, fmt.Errorf("load server cert (запустіть `go run ./cmd/gencerts`): %w", err)
	}
	pool, err := loadPool(filepath.Join(dir, "ca.pem"))
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// ClientConfig: клієнт перевіряє сервер за ca.pem і (якщо certName != "none")
// пред'являє власний сертифікат <certName>.pem / <certName>-key.pem.
//
//	certName = "client"        — довірений клієнт (успіх)
//	certName = "rogue-client"  — сертифікат від стороннього CA (сервер відхилить)
//	certName = "none"          — звичайний TLS без клієнтського сертифіката (сервер відхилить)
func ClientConfig(dir, certName string) (*tls.Config, error) {
	pool, err := loadPool(filepath.Join(dir, "ca.pem"))
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	if certName != "none" {
		cert, err := tls.LoadX509KeyPair(
			filepath.Join(dir, certName+".pem"), filepath.Join(dir, certName+"-key.pem"))
		if err != nil {
			return nil, fmt.Errorf("load client cert %q (запустіть `go run ./cmd/gencerts`): %w", certName, err)
		}
		// GetClientCertificate примусово надсилає сертифікат. Без цього Go-клієнт мовчки
		// НЕ надсилає сертифікат, виданий CA, якого сервер не оголошував, — і ми б бачили
		// лише "client didn't provide a certificate" замість справжньої перевірки ланцюжка довіри.
		cfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &cert, nil
		}
	}
	return cfg, nil
}

func loadPool(caFile string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read CA %s: %w", caFile, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no certificates found in %s", caFile)
	}
	return pool, nil
}
