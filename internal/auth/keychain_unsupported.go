//go:build (!darwin && !linux) || (darwin && !cgo)

package auth

import "errors"

type platformVault struct{}

func (platformVault) Get(string, string) ([]byte, error) {
	return nil, errors.New("OS store unavailable")
}
func (platformVault) Set(string, string, []byte) error { return errors.New("OS store unavailable") }
func (platformVault) Delete(string, string) error      { return errors.New("OS store unavailable") }
