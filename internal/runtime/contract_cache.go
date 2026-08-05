package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

type cachedContract struct {
	ETag     string            `json:"etag"`
	Contract protocol.Contract `json:"contract"`
}

func loadContractCache(filePath string) (cachedContract, error) {
	body, err := os.ReadFile(filePath)
	if errors.Is(err, os.ErrNotExist) {
		return cachedContract{}, nil
	}
	if err != nil {
		return cachedContract{}, err
	}
	var value cachedContract
	if err := json.Unmarshal(body, &value); err != nil {
		return cachedContract{}, errors.New("contract cache is invalid")
	}
	if err := protocol.ValidateContract(value.Contract); err != nil {
		return cachedContract{}, fmt.Errorf("cached contract is invalid: %w", err)
	}
	digest, err := protocol.BundleDigest()
	if err != nil || value.Contract.SchemaBundleDigest != digest {
		return cachedContract{}, errors.New("cached contract schema is incompatible")
	}
	return value, nil
}

func writeContractCache(filePath string, value cachedContract) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		return err
	}
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(filePath), ".contract-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
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
	return os.Rename(name, filePath)
}
