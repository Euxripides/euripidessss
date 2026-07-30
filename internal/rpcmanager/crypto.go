package rpcmanager

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

type secureStore struct {
	key []byte
}

func openSecureStore(secureDir string) (*secureStore, error) {
	if err := os.MkdirAll(secureDir, 0700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(secureDir, "rpc_master.dpapi")
	protected, err := os.ReadFile(keyPath)
	switch {
	case err == nil:
		key, decryptErr := unprotectLocal(protected)
		if decryptErr != nil {
			return nil, decryptErr
		}
		if len(key) != 32 {
			return nil, errors.New("RPC master key length is invalid")
		}
		return &secureStore{key: key}, nil
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	protected, err = protectLocal(key)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, protected, 0600); err != nil {
		return nil, err
	}
	return &secureStore{key: key}, nil
}

func (s *secureStore) encrypt(plain string) ([]byte, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, []byte(plain), nil), nil
}

func (s *secureStore) decrypt(ciphertext []byte) (string, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(ciphertext) < aead.NonceSize() {
		return "", errors.New("RPC ciphertext is invalid")
	}
	plain, err := aead.Open(nil, ciphertext[:aead.NonceSize()], ciphertext[aead.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
