package gonote

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5" // Required by PRONOTE's protocol, not used for password storage.
	"encoding/hex"
	"fmt"
)

type sessionCipher struct{ key, iv []byte }

func (s sessionCipher) block() (cipher.Block, []byte) {
	key := md5.Sum(s.key)
	iv := make([]byte, aes.BlockSize)
	if len(s.iv) > 0 && !bytes.Equal(s.iv, make([]byte, len(s.iv))) {
		h := md5.Sum(s.iv)
		copy(iv, h[:])
	}
	b, _ := aes.NewCipher(key[:])
	return b, iv
}

func (s sessionCipher) encrypt(b []byte) []byte {
	block, iv := s.block()
	n := aes.BlockSize - len(b)%aes.BlockSize
	b = append(bytes.Clone(b), bytes.Repeat([]byte{byte(n)}, n)...)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(b, b)
	return b
}

func (s sessionCipher) decrypt(b []byte) ([]byte, error) {
	if len(b) == 0 || len(b)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("%w : bloc chiffré invalide", ErrProtocol)
	}
	block, iv := s.block()
	b = bytes.Clone(b)
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(b, b)
	n := int(b[len(b)-1])
	if n < 1 || n > aes.BlockSize || !bytes.Equal(b[len(b)-n:], bytes.Repeat([]byte{byte(n)}, n)) {
		return nil, ErrCredentials
	}
	return b[:len(b)-n], nil
}

func (s sessionCipher) encryptText(v string) string { return hex.EncodeToString(s.encrypt([]byte(v))) }
func (s sessionCipher) decryptText(v string) ([]byte, error) {
	b, err := hex.DecodeString(v)
	if err != nil {
		return nil, ErrProtocol
	}
	return s.decrypt(b)
}
