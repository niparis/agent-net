package gateway

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

type authority struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte
}

// Fingerprint is the SHA-256 of a CA certificate's DER bytes, in hex.
func Fingerprint(certPEM []byte) (string, error) { return protocol.CertFingerprint(certPEM) }

// CAFingerprint reads the CA in a gateway data dir and returns its fingerprint.
func CAFingerprint(dataDir string) (string, error) {
	pemBytes, err := os.ReadFile(filepath.Join(dataDir, "ca.pem"))
	if err != nil {
		return "", fmt.Errorf("no CA in %s (start the gateway once first): %w", dataDir, err)
	}
	return Fingerprint(pemBytes)
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	return n
}

func loadOrCreateCA(dir string) (*authority, error) {
	certPath, keyPath := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca.key")
	if certPEM, err := os.ReadFile(certPath); err == nil {
		keyPEM, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, err
		}
		cb, _ := pem.Decode(certPEM)
		kb, _ := pem.Decode(keyPEM)
		if cb == nil || kb == nil {
			return nil, errors.New("corrupt CA files")
		}
		cert, err := x509.ParseCertificate(cb.Bytes)
		if err != nil {
			return nil, err
		}
		key, err := x509.ParseECPrivateKey(kb.Bytes)
		if err != nil {
			return nil, err
		}
		return &authority{cert, key, certPEM}, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "agent-net gateway CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, err
	}
	return &authority{cert, key, certPEM}, nil
}

// loadOrCreateServerKey returns the persisted server private key. The
// certificate changes on every start but the key does not, so the public key
// pin that `agent-net invite` prints stays valid.
func loadOrCreateServerKey(dir string) (*ecdsa.PrivateKey, error) {
	path := filepath.Join(dir, "server-key.pem")
	if keyPEM, err := os.ReadFile(path); err == nil {
		kb, _ := pem.Decode(keyPEM)
		if kb == nil {
			return nil, errors.New("corrupt server-key.pem")
		}
		return x509.ParseECPrivateKey(kb.Bytes)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return key, os.WriteFile(path, keyPEM, 0o600)
}

// PublicKeyPin is the value for `curl --pinnedpubkey`: "sha256//" and the
// base64 of the SHA-256 of the key's SubjectPublicKeyInfo DER bytes.
func PublicKeyPin(pub crypto.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(der)
	return "sha256//" + base64.StdEncoding.EncodeToString(sum[:]), nil
}

// ServerPin reads (or creates) the server key in a gateway data dir and
// returns its curl pin.
func ServerPin(dataDir string) (string, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", err
	}
	key, err := loadOrCreateServerKey(dataDir)
	if err != nil {
		return "", err
	}
	return PublicKeyPin(&key.PublicKey)
}

// serverCert signs a fresh server certificate for the persisted key. It is
// regenerated on every start so a changed host name or address is picked up.
func (a *authority) serverCert(key *ecdsa.PrivateKey, sans []string) (tls.Certificate, error) {
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "agent-net gateway"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	add := func(h string) {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else if h != "" {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	add("localhost")
	add("127.0.0.1")
	add("::1")
	if h, err := os.Hostname(); err == nil {
		add(h)
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, ad := range addrs {
			if ipn, ok := ad.(*net.IPNet); ok {
				tmpl.IPAddresses = append(tmpl.IPAddresses, ipn.IP)
			}
		}
	}
	for _, s := range sans {
		add(s)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
