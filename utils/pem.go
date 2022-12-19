package utils

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
)

func GeneratePrivateKeyBytes() []byte {
	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)

	derStream := x509.MarshalPKCS1PrivateKey(privateKey)
	block := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: derStream,
	}
	return pem.EncodeToMemory(block)
}

func LoadPrivateKeyFromBytes(privateKeyData []byte) (*rsa.PrivateKey, error) {
	privateKeyBlock, _ := pem.Decode(privateKeyData)
	return x509.ParsePKCS1PrivateKey(privateKeyBlock.Bytes)
}

func ExportPublicKeyBytes(privateKey *rsa.PrivateKey) []byte {
	publicKey := &privateKey.PublicKey
	derPkix, _ := x509.MarshalPKIXPublicKey(publicKey)
	block := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: derPkix,
	}
	return pem.EncodeToMemory(block)
}
