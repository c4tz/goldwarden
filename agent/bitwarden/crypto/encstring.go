package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"strconv"
)

type EncString struct {
	Type        EncStringType
	IV, CT, MAC []byte
}

type EncStringType int

const (
	AesCbc256_B64                     EncStringType = 0
	AesCbc128_HmacSha256_B64          EncStringType = 1
	AesCbc256_HmacSha256_B64          EncStringType = 2
	Rsa2048_OaepSha256_B64            EncStringType = 3
	Rsa2048_OaepSha1_B64              EncStringType = 4
	Rsa2048_OaepSha256_HmacSha256_B64 EncStringType = 5
	Rsa2048_OaepSha1_HmacSha256_B64   EncStringType = 6
)

func (t EncStringType) HasMAC() bool {
	return t != AesCbc256_B64
}

func (s *EncString) UnmarshalText(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	i := bytes.IndexByte(data, '.')
	if i < 0 {
		return errors.New("invalid cipher string format")
	}

	typStr := string(data[:i])
	var err error
	if t, err := strconv.Atoi(typStr); err != nil {
		return errors.New("invalid cipher string type")
	} else {
		s.Type = EncStringType(t)
	}

	switch s.Type {
	case AesCbc128_HmacSha256_B64, AesCbc256_HmacSha256_B64, AesCbc256_B64:
	default:
		return errors.New("invalid cipher string type")
	}

	data = data[i+1:]
	parts := bytes.Split(data, []byte("|"))
	if len(parts) != 3 {
		return errors.New("invalid cipher string format")
	}

	if s.IV, err = b64decode(parts[0]); err != nil {
		return err
	}
	if s.CT, err = b64decode(parts[1]); err != nil {
		return err
	}
	if s.Type.HasMAC() {
		if s.MAC, err = b64decode(parts[2]); err != nil {
			return err
		}
	}
	return nil
}

func (s EncString) MarshalText() ([]byte, error) {
	if s.Type == 0 {
		return nil, nil
	}

	var buf bytes.Buffer
	buf.WriteString(strconv.Itoa(int(s.Type)))
	buf.WriteByte('.')
	buf.Write(b64encode(s.IV))
	buf.WriteByte('|')
	buf.Write(b64encode(s.CT))
	if s.Type.HasMAC() {
		buf.WriteByte('|')
		buf.Write(b64encode(s.MAC))
	}
	return buf.Bytes(), nil
}

func (s EncString) IsNull() bool {
	return len(s.IV) == 0 && len(s.CT) == 0 && len(s.MAC) == 0
}

func b64decode(src []byte) ([]byte, error) {
	dst := make([]byte, b64enc.DecodedLen(len(src)))
	n, err := b64enc.Decode(dst, src)
	if err != nil {
		return nil, err
	}
	dst = dst[:n]
	return dst, nil
}

func b64encode(src []byte) []byte {
	dst := make([]byte, b64enc.EncodedLen(len(src)))
	b64enc.Encode(dst, src)
	return dst
}

func DecryptWith(s EncString, key SymmetricEncryptionKey) ([]byte, error) {
	encKeyData, err := key.encKey.Open()
	if err != nil {
		return nil, err
	}
	macKeyData, err := key.macKey.Open()
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(encKeyData.Data())
	if err != nil {
		return nil, err
	}

	switch s.Type {
	case AesCbc256_B64, AesCbc256_HmacSha256_B64:
		break
	default:
		return nil, fmt.Errorf("decrypt: unsupported cipher type %q", s.Type)
	}

	if s.Type == AesCbc256_HmacSha256_B64 {
		if len(s.MAC) == 0 || len(macKeyData.Data()) == 0 {
			return nil, fmt.Errorf("decrypt: cipher string type expects a MAC")
		}
		var msg []byte
		msg = append(msg, s.IV...)
		msg = append(msg, s.CT...)
		if !isMacValid(msg, s.MAC, macKeyData.Data()) {
			return nil, fmt.Errorf("decrypt: MAC mismatch")
		}
	}

	mode := cipher.NewCBCDecrypter(block, s.IV)
	dst := make([]byte, len(s.CT))
	mode.CryptBlocks(dst, s.CT)
	dst, err = unpadPKCS7(dst, aes.BlockSize)
	if err != nil {
		return nil, err
	}
	return dst, nil
}

func EncryptWith(data []byte, typ EncStringType, key SymmetricEncryptionKey) (EncString, error) {
	encKeyData, err := key.encKey.Open()
	if err != nil {
		return EncString{}, err
	}
	macKeyData, err := key.macKey.Open()
	if err != nil {
		return EncString{}, err
	}

	s := EncString{}
	switch typ {
	case AesCbc256_B64, AesCbc256_HmacSha256_B64:
	default:
		return s, fmt.Errorf("encrypt: unsupported cipher type %q", s.Type)
	}
	s.Type = typ
	data = padPKCS7(data, aes.BlockSize)

	block, err := aes.NewCipher(encKeyData.Bytes())
	if err != nil {
		return s, err
	}
	s.IV = make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(cryptorand.Reader, s.IV); err != nil {
		return s, err
	}
	s.CT = make([]byte, len(data))
	mode := cipher.NewCBCEncrypter(block, s.IV)
	mode.CryptBlocks(s.CT, data)

	if typ == AesCbc256_HmacSha256_B64 {
		if len(macKeyData.Bytes()) == 0 {
			return s, fmt.Errorf("encrypt: cipher string type expects a MAC")
		}
		var macMessage []byte
		macMessage = append(macMessage, s.IV...)
		macMessage = append(macMessage, s.CT...)
		mac := hmac.New(sha256.New, macKeyData.Bytes())
		mac.Write(macMessage)
		s.MAC = mac.Sum(nil)
	}

	return s, nil
}

func GenerateAsymmetric() (AsymmetricEncryptionKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return AsymmetricEncryptionKey{}, err
	}

	encKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return AsymmetricEncryptionKey{}, err
	}

	return AssymmetricEncryptionKeyFromBytes(encKey)
}

func DecryptWithAsymmetric(s []byte, asymmetrickey AsymmetricEncryptionKey) ([]byte, error) {
	key, err := asymmetrickey.encKey.Open()
	if err != nil {
		return nil, err
	}

	parsedKey, err := x509.ParsePKCS8PrivateKey(key.Bytes())
	if err != nil {
		return nil, err
	}

	rawKey, err := b64decode(s[2:])
	if err != nil {
		return nil, err
	}

	res, err := rsa.DecryptOAEP(sha1.New(), rand.Reader, parsedKey.(*rsa.PrivateKey), rawKey, nil)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func EncryptWithAsymmetric(s []byte, asymmbetrickey AsymmetricEncryptionKey) ([]byte, error) {
	key, err := asymmbetrickey.encKey.Open()
	if err != nil {
		return nil, err
	}

	parsedKey, err := x509.ParsePKIXPublicKey(key.Bytes())
	if err != nil {
		return nil, err
	}

	res, err := rsa.EncryptOAEP(sha1.New(), rand.Reader, parsedKey.(*rsa.PublicKey), s, nil)
	if err != nil {
		return nil, err
	}

	resB64 := b64encode(res)
	res = append([]byte("4."), resB64...)

	return res, nil
}
