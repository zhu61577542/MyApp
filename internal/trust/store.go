package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"myapp/internal/identity"
)

var (
	ErrNotTrusted = errors.New("设备不受信任")
	ErrRevoked    = errors.New("设备信任已撤销")
)

type Record struct {
	DeviceID    string    `json:"device_id"`
	DeviceName  string    `json:"device_name"`
	Certificate []byte    `json:"certificate_der"`
	Fingerprint string    `json:"sha256_fingerprint"`
	PairedAt    time.Time `json:"paired_at"`
}

type revocation struct {
	DeviceID  string    `json:"device_id"`
	RevokedAt time.Time `json:"revoked_at"`
}

type Store struct {
	directory string
}

func New(directory string) Store {
	return Store{directory: directory}
}

func (s Store) AddNew(certificateDER []byte, now time.Time) (Record, error) {
	deviceID, cert, err := identity.ValidateCertificate(certificateDER, now)
	if err != nil {
		return Record{}, err
	}
	if err := validateDeviceID(deviceID); err != nil {
		return Record{}, err
	}
	if _, err := os.Stat(s.revokedPath(deviceID)); err == nil {
		return Record{}, ErrRevoked
	} else if !errors.Is(err, os.ErrNotExist) {
		return Record{}, err
	}
	sum := sha256.Sum256(certificateDER)
	record := Record{
		DeviceID:    deviceID,
		DeviceName:  cert.Subject.OrganizationalUnit[0],
		Certificate: append([]byte(nil), certificateDER...),
		Fingerprint: hex.EncodeToString(sum[:]),
		PairedAt:    now.UTC(),
	}
	if err := os.MkdirAll(s.trustedDirectory(), 0700); err != nil {
		return Record{}, err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return Record{}, err
	}
	data = append(data, '\n')
	if err := writeExclusive(s.trustedPath(deviceID), data); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (s Store) Get(deviceID string) (Record, error) {
	if err := validateDeviceID(deviceID); err != nil {
		return Record{}, err
	}
	if _, err := os.Stat(s.revokedPath(deviceID)); err == nil {
		return Record{}, ErrRevoked
	} else if !errors.Is(err, os.ErrNotExist) {
		return Record{}, err
	}
	data, err := os.ReadFile(s.trustedPath(deviceID))
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, ErrNotTrusted
	}
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, err
	}
	if record.DeviceID != deviceID {
		return Record{}, errors.New("信任记录设备 ID 不一致")
	}
	sum := sha256.Sum256(record.Certificate)
	if record.Fingerprint != hex.EncodeToString(sum[:]) {
		return Record{}, errors.New("信任记录指纹不一致")
	}
	return record, nil
}

func (s Store) List() ([]Record, error) {
	files, err := os.ReadDir(s.trustedDirectory())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []Record
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(file.Name(), ".json")
		record, err := s.Get(id)
		if errors.Is(err, ErrRevoked) {
			continue
		}
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].DeviceName < records[j].DeviceName })
	return records, nil
}

// 仅在新一轮双方确认成功后调用。
func (s Store) AddAfterConfirmation(certificateDER []byte, now time.Time) (Record, error) {
	id, cert, err := identity.ValidateCertificate(certificateDER, now)
	if err != nil {
		return Record{}, err
	}
	if err := validateDeviceID(id); err != nil {
		return Record{}, err
	}
	if old, err := s.VerifyCertificate(certificateDER, now); err == nil {
		return old, nil
	}
	sum := sha256.Sum256(certificateDER)
	record := Record{DeviceID: id, DeviceName: cert.Subject.OrganizationalUnit[0], Certificate: append([]byte(nil), certificateDER...), Fingerprint: hex.EncodeToString(sum[:]), PairedAt: now.UTC()}
	if err := os.MkdirAll(s.trustedDirectory(), 0700); err != nil {
		return Record{}, err
	}
	file, err := os.CreateTemp(s.trustedDirectory(), ".paired-*")
	if err != nil {
		return Record{}, err
	}
	defer os.Remove(file.Name())
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		file.Close()
		return Record{}, err
	}
	_, writeErr := file.Write(append(data, '\n'))
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return Record{}, err
	}
	if err := os.Rename(file.Name(), s.trustedPath(id)); err != nil {
		return Record{}, err
	}
	if err := os.Remove(s.revokedPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Record{}, err
	}
	return record, nil
}

func (s Store) VerifyCertificate(certificateDER []byte, now time.Time) (Record, error) {
	deviceID, _, err := identity.ValidateCertificate(certificateDER, now)
	if err != nil {
		return Record{}, err
	}
	record, err := s.Get(deviceID)
	if err != nil {
		return Record{}, err
	}
	want := sha256.Sum256(record.Certificate)
	got := sha256.Sum256(certificateDER)
	if want != got {
		return Record{}, errors.New("设备证书与已配对指纹不一致")
	}
	return record, nil
}

func (s Store) Revoke(deviceID string, now time.Time) error {
	if err := validateDeviceID(deviceID); err != nil {
		return err
	}
	if _, err := s.Get(deviceID); err != nil {
		return err
	}
	if err := os.MkdirAll(s.revokedDirectory(), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(revocation{DeviceID: deviceID, RevokedAt: now.UTC()}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeExclusive(s.revokedPath(deviceID), append(data, '\n')); err != nil {
		return err
	}
	if err := os.Remove(s.trustedPath(deviceID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s Store) trustedDirectory() string {
	return filepath.Join(s.directory, "trusted")
}

func (s Store) revokedDirectory() string {
	return filepath.Join(s.directory, "revoked")
}

func (s Store) trustedPath(deviceID string) string {
	return filepath.Join(s.trustedDirectory(), deviceID+".json")
}

func (s Store) revokedPath(deviceID string) string {
	return filepath.Join(s.revokedDirectory(), deviceID+".json")
}

func validateDeviceID(deviceID string) error {
	if len(deviceID) != 52 {
		return errors.New("设备 ID 长度无效")
	}
	for _, character := range deviceID {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz234567", character) {
			return fmt.Errorf("设备 ID 包含无效字符: %q", character)
		}
	}
	return nil
}

func writeExclusive(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	return errors.Join(writeErr, file.Sync(), file.Close())
}
