package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"myapp/internal/identity"
	"myapp/internal/trust"
)

const ALPN = "myapp/1"

func ServerTLS(local identity.Identity, store trust.Store) *tls.Config {
	return &tls.Config{
		Certificates:          []tls.Certificate{local.TLSCertificate()},
		MinVersion:            tls.VersionTLS13,
		ClientAuth:            tls.RequireAnyClientCert,
		NextProtos:            []string{ALPN},
		VerifyPeerCertificate: verifyPeer(store, ""),
	}
}

func ClientTLS(local identity.Identity, expectedPeerID string, store trust.Store) *tls.Config {
	return &tls.Config{
		Certificates:          []tls.Certificate{local.TLSCertificate()},
		MinVersion:            tls.VersionTLS13,
		NextProtos:            []string{ALPN},
		InsecureSkipVerify:    true,
		VerifyPeerCertificate: verifyPeer(store, expectedPeerID),
	}
}

func PeerDeviceID(state tls.ConnectionState) (string, error) {
	if len(state.PeerCertificates) != 1 {
		return "", errors.New("对端必须提供一个设备证书")
	}
	deviceID, _, err := identity.ValidateCertificate(state.PeerCertificates[0].Raw, time.Now())
	return deviceID, err
}

func verifyPeer(store trust.Store, expectedPeerID string) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) != 1 {
			return errors.New("对端必须提供一个自签名设备证书")
		}
		record, err := store.VerifyCertificate(rawCerts[0], time.Now())
		if err != nil {
			return fmt.Errorf("验证对端信任: %w", err)
		}
		if expectedPeerID != "" && record.DeviceID != expectedPeerID {
			return errors.New("连接的设备不是预期目标")
		}
		return nil
	}
}
