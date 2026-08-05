package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const stateVersion = 1

type persisted struct {
	Version    int    `json:"version"`
	PrivateKey string `json:"privateKey"`
	DeviceID   uint64 `json:"deviceId,omitempty"`
	Sequence   uint64 `json:"sequence"`
}

type Identity struct {
	mu         sync.Mutex
	filePath   string
	privateKey ed25519.PrivateKey
	deviceID   uint64
	sequence   uint64
}

func LoadOrCreate(filePath string) (*Identity, error) {
	body, err := os.ReadFile(filePath)
	if errors.Is(err, os.ErrNotExist) {
		_, privateKey, generateErr := ed25519.GenerateKey(rand.Reader)
		if generateErr != nil {
			return nil, fmt.Errorf("generate identity: %w", generateErr)
		}
		identity := &Identity{filePath: filePath, privateKey: privateKey}
		if writeErr := identity.persistLocked(); writeErr != nil {
			return nil, writeErr
		}
		return identity, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read identity: %w", err)
	}
	var value persisted
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, fmt.Errorf("decode identity: %w", err)
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(value.PrivateKey)
	if err != nil || value.Version != stateVersion || len(decoded) != ed25519.PrivateKeySize {
		return nil, errors.New("identity file is invalid")
	}
	if value.DeviceID > 1<<53-1 {
		return nil, errors.New("identity device id is invalid")
	}
	if info, statErr := os.Stat(filePath); statErr != nil || info.Mode().Perm() != 0o600 {
		return nil, errors.New("identity file permissions must be 0600")
	}
	return &Identity{
		filePath: filePath, privateKey: ed25519.PrivateKey(decoded),
		deviceID: value.DeviceID, sequence: value.Sequence,
	}, nil
}

func (i *Identity) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(i.filePath), 0o700); err != nil {
		return fmt.Errorf("create identity directory: %w", err)
	}
	value := persisted{
		Version: stateVersion, PrivateKey: base64.StdEncoding.EncodeToString(i.privateKey),
		DeviceID: i.deviceID, Sequence: i.sequence,
	}
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode identity: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(i.filePath), ".identity-*")
	if err != nil {
		return fmt.Errorf("create temporary identity: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(body, '\n')); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, i.filePath); err != nil {
		return fmt.Errorf("replace identity: %w", err)
	}
	return os.Chmod(i.filePath, 0o600)
}

func (i *Identity) PublicKeyBase64() (string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	encoded, err := x509.MarshalPKIXPublicKey(i.privateKey.Public())
	if err != nil {
		return "", fmt.Errorf("marshal public key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(encoded), nil
}

func (i *Identity) PrivateKey() ed25519.PrivateKey {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append(ed25519.PrivateKey(nil), i.privateKey...)
}

func (i *Identity) DeviceID() uint64 {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.deviceID
}

func (i *Identity) Activate(deviceID uint64) error {
	if deviceID == 0 || deviceID > 1<<53-1 {
		return errors.New("device id must be a positive safe integer")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.deviceID != 0 && i.deviceID != deviceID {
		return errors.New("identity is already bound to another device")
	}
	i.deviceID = deviceID
	return i.persistLocked()
}

func (i *Identity) ReserveSequence() (uint64, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.deviceID == 0 {
		return 0, errors.New("identity is not activated")
	}
	if i.sequence == ^uint64(0) {
		return 0, errors.New("identity sequence is exhausted")
	}
	i.sequence++
	if err := i.persistLocked(); err != nil {
		i.sequence--
		return 0, err
	}
	return i.sequence, nil
}
