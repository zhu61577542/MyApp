package pairing

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"myapp/internal/identity"
)

const (
	Version       = 1
	OfferLifetime = 2 * time.Minute
)

type Offer struct {
	Version         int    `json:"version"`
	DeviceID        string `json:"device_id"`
	DeviceName      string `json:"device_name"`
	CertificateDER  []byte `json:"certificate_der"`
	EphemeralPublic []byte `json:"ephemeral_public"`
	Nonce           []byte `json:"nonce"`
	CreatedUnix     int64  `json:"created_unix"`
	Signature       []byte `json:"signature"`
}

type LocalOffer struct {
	Offer   Offer
	private *ecdh.PrivateKey
}

type Match struct {
	RemoteDeviceID   string
	RemoteDeviceName string
	RemoteCertDER    []byte
	Code             string
	Confirmation     []byte
}

func New(local identity.Identity, now time.Time) (LocalOffer, error) {
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return LocalOffer{}, err
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return LocalOffer{}, err
	}
	name := local.Cert.Subject.OrganizationalUnit[0]
	offer := Offer{
		Version:         Version,
		DeviceID:        local.DeviceID,
		DeviceName:      name,
		CertificateDER:  append([]byte(nil), local.CertDER...),
		EphemeralPublic: privateKey.PublicKey().Bytes(),
		Nonce:           nonce,
		CreatedUnix:     now.Unix(),
	}
	unsigned, err := canonicalUnsigned(offer)
	if err != nil {
		return LocalOffer{}, err
	}
	offer.Signature = ed25519.Sign(local.PrivateKey, unsigned)
	return LocalOffer{Offer: offer, private: privateKey}, nil
}

func (local LocalOffer) Match(remote Offer, now time.Time) (Match, error) {
	if local.private == nil {
		return Match{}, errors.New("本地配对私钥不存在")
	}
	if err := ValidateOffer(local.Offer, now); err != nil {
		return Match{}, fmt.Errorf("本地配对信息无效: %w", err)
	}
	if err := ValidateOffer(remote, now); err != nil {
		return Match{}, fmt.Errorf("远端配对信息无效: %w", err)
	}
	if local.Offer.DeviceID == remote.DeviceID {
		return Match{}, errors.New("不能与本机配对")
	}
	remotePublic, err := ecdh.X25519().NewPublicKey(remote.EphemeralPublic)
	if err != nil {
		return Match{}, err
	}
	shared, err := local.private.ECDH(remotePublic)
	if err != nil {
		return Match{}, err
	}
	transcript, err := transcript(local.Offer, remote)
	if err != nil {
		return Match{}, err
	}
	codeMAC := hmac.New(sha256.New, shared)
	codeMAC.Write([]byte("MYAPP-PAIR-CODE\x00"))
	codeMAC.Write(transcript)
	value := binary.BigEndian.Uint32(codeMAC.Sum(nil)[:4]) % 1000000
	confirmMAC := hmac.New(sha256.New, shared)
	confirmMAC.Write([]byte("MYAPP-PAIR-CONFIRM\x00"))
	confirmMAC.Write(transcript)
	return Match{
		RemoteDeviceID:   remote.DeviceID,
		RemoteDeviceName: remote.DeviceName,
		RemoteCertDER:    append([]byte(nil), remote.CertificateDER...),
		Code:             fmt.Sprintf("%06d", value),
		Confirmation:     confirmMAC.Sum(nil),
	}, nil
}

func ValidateOffer(offer Offer, now time.Time) error {
	if offer.Version != Version {
		return errors.New("配对版本不兼容")
	}
	created := time.Unix(offer.CreatedUnix, 0)
	if created.After(now.Add(30*time.Second)) || now.Sub(created) > OfferLifetime {
		return errors.New("配对信息已过期或时间异常")
	}
	if len(offer.EphemeralPublic) != 32 || len(offer.Nonce) != 32 || len(offer.Signature) != ed25519.SignatureSize {
		return errors.New("配对字段长度无效")
	}
	deviceID, cert, err := identity.ValidateCertificate(offer.CertificateDER, now)
	if err != nil {
		return err
	}
	if deviceID != offer.DeviceID || cert.Subject.OrganizationalUnit[0] != offer.DeviceName {
		return errors.New("配对设备信息与证书不一致")
	}
	unsigned, err := canonicalUnsigned(offer)
	if err != nil {
		return err
	}
	publicKey, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok || !ed25519.Verify(publicKey, unsigned, offer.Signature) {
		return errors.New("配对签名无效")
	}
	return nil
}

func canonicalUnsigned(offer Offer) ([]byte, error) {
	offer.Signature = nil
	return json.Marshal(offer)
}

func transcript(first, second Offer) ([]byte, error) {
	a, err := json.Marshal(first)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(second)
	if err != nil {
		return nil, err
	}
	if first.DeviceID > second.DeviceID {
		a, b = b, a
	}
	var result bytes.Buffer
	for _, value := range [][]byte{a, b} {
		if err := binary.Write(&result, binary.BigEndian, uint32(len(value))); err != nil {
			return nil, err
		}
		result.Write(value)
	}
	return result.Bytes(), nil
}
