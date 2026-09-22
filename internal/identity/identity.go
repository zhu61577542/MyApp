package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base32"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	KeyFile  = "identity.key"
	CertFile = "identity.crt"
)

type Identity struct {
	DeviceID   string
	PrivateKey ed25519.PrivateKey
	CertDER    []byte
	Cert       *x509.Certificate
}

func DeviceID(publicKey ed25519.PublicKey) string {
	sum := sha256.Sum256(publicKey)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))
}

func Generate(deviceName string, now time.Time) (Identity, error) {
	if strings.TrimSpace(deviceName) == "" || strings.TrimSpace(deviceName) != deviceName {
		return Identity{}, errors.New("设备名称不能为空或包含首尾空白")
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Identity{}, err
	}
	id := DeviceID(publicKey)
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return Identity{}, err
	}
	deviceURI, err := url.Parse("myapp://device/" + id)
	if err != nil {
		return Identity{}, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:         id,
			OrganizationalUnit: []string{deviceName},
			Organization:       []string{"MyApp Device"},
		},
		NotBefore:             now.Add(-5 * time.Minute).UTC(),
		NotAfter:              now.AddDate(10, 0, 0).UTC(),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		URIs:                  []*url.URL{deviceURI},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		return Identity{}, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return Identity{}, err
	}
	return Identity{DeviceID: id, PrivateKey: privateKey, CertDER: der, Cert: cert}, nil
}

func SaveNew(directory string, identity Identity) error {
	if err := verify(identity); err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	keyPath := filepath.Join(directory, KeyFile)
	certPath := filepath.Join(directory, CertFile)
	if _, err := os.Stat(keyPath); err == nil || !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("身份文件已存在: %s", keyPath)
		}
		return err
	}
	if _, err := os.Stat(certPath); err == nil || !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("身份文件已存在: %s", certPath)
		}
		return err
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(identity.PrivateKey)
	if err != nil {
		return err
	}
	if err := writeExclusive(keyPath, 0600, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})); err != nil {
		return err
	}
	if err := writeExclusive(certPath, 0644, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: identity.CertDER})); err != nil {
		os.Remove(keyPath)
		return err
	}
	return nil
}

func Load(directory string) (Identity, error) {
	keyPEM, err := os.ReadFile(filepath.Join(directory, KeyFile))
	if err != nil {
		return Identity{}, err
	}
	certPEM, err := os.ReadFile(filepath.Join(directory, CertFile))
	if err != nil {
		return Identity{}, err
	}
	keyBlock, rest := pem.Decode(keyPEM)
	if keyBlock == nil || len(rest) != 0 || keyBlock.Type != "PRIVATE KEY" {
		return Identity{}, errors.New("私钥 PEM 无效")
	}
	parsedKey, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return Identity{}, err
	}
	privateKey, ok := parsedKey.(ed25519.PrivateKey)
	if !ok {
		return Identity{}, errors.New("私钥不是 Ed25519")
	}
	certBlock, rest := pem.Decode(certPEM)
	if certBlock == nil || len(rest) != 0 || certBlock.Type != "CERTIFICATE" {
		return Identity{}, errors.New("证书 PEM 无效")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return Identity{}, err
	}
	identity := Identity{DeviceID: cert.Subject.CommonName, PrivateKey: privateKey, CertDER: certBlock.Bytes, Cert: cert}
	if err := verify(identity); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

func (i Identity) TLSCertificate() tls.Certificate {
	return tls.Certificate{Certificate: [][]byte{i.CertDER}, PrivateKey: i.PrivateKey, Leaf: i.Cert}
}

func ValidateCertificate(der []byte, now time.Time) (string, *x509.Certificate, error) {
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return "", nil, err
	}
	publicKey, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok {
		return "", nil, errors.New("设备证书不是 Ed25519")
	}
	deviceID := DeviceID(publicKey)
	if cert.Subject.CommonName != deviceID {
		return "", nil, errors.New("设备证书 ID 与公钥不匹配")
	}
	if len(cert.Subject.OrganizationalUnit) != 1 || strings.TrimSpace(cert.Subject.OrganizationalUnit[0]) == "" {
		return "", nil, errors.New("设备证书缺少设备名称")
	}
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return "", nil, errors.New("设备证书不在有效期内")
	}
	if err := cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature); err != nil {
		return "", nil, fmt.Errorf("设备证书自签名无效: %w", err)
	}
	return deviceID, cert, nil
}

func verify(identity Identity) error {
	if len(identity.PrivateKey) != ed25519.PrivateKeySize || identity.Cert == nil || len(identity.CertDER) == 0 {
		return errors.New("身份内容不完整")
	}
	publicKey, ok := identity.Cert.PublicKey.(ed25519.PublicKey)
	if !ok || !publicKey.Equal(identity.PrivateKey.Public()) {
		return errors.New("证书与私钥不匹配")
	}
	expected, _, err := ValidateCertificate(identity.CertDER, identity.Cert.NotBefore.Add(time.Second))
	if err != nil {
		return err
	}
	if identity.DeviceID != expected {
		return errors.New("设备 ID 与公钥不匹配")
	}
	return nil
}

func writeExclusive(path string, mode os.FileMode, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	return errors.Join(writeErr, file.Sync(), file.Close())
}
