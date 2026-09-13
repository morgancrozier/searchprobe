//go:build darwin && cgo

package auth

import (
	kc "github.com/keybase/go-keychain"
	"os"
)

type platformVault struct{}

func (platformVault) Get(service, account string) ([]byte, error) {
	q := kc.NewItem()
	q.SetSecClass(kc.SecClassGenericPassword)
	q.SetService(service)
	q.SetAccount(account)
	q.SetMatchLimit(kc.MatchLimitOne)
	q.SetReturnData(true)
	r, err := kc.QueryItem(q)
	if err == kc.ErrorItemNotFound {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	if len(r) != 1 {
		return nil, os.ErrNotExist
	}
	return r[0].Data, nil
}
func (platformVault) Set(service, account string, data []byte) error {
	item := kc.NewGenericPassword(service, account, "SearchProbe Google credentials", data, "")
	item.SetSynchronizable(kc.SynchronizableNo)
	item.SetAccessible(kc.AccessibleWhenUnlocked)
	return kc.AddItem(item)
}
func (platformVault) Delete(service, account string) error {
	err := kc.DeleteGenericPasswordItem(service, account)
	if err == kc.ErrorItemNotFound {
		return nil
	}
	return err
}
