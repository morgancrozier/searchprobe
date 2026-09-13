package auth

import (
	"errors"
	kr "github.com/zalando/go-keyring"
	"os"
)

type platformVault struct{}

func (platformVault) Get(service, account string) ([]byte, error) {
	v, err := kr.Get(service, account)
	if errors.Is(err, kr.ErrNotFound) {
		return nil, os.ErrNotExist
	}
	return []byte(v), err
}
func (platformVault) Set(service, account string, data []byte) error {
	return kr.Set(service, account, string(data))
}
func (platformVault) Delete(service, account string) error {
	err := kr.Delete(service, account)
	if errors.Is(err, kr.ErrNotFound) {
		return nil
	}
	return err
}
