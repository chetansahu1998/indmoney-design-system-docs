// Command genkey generates Ed25519 + AES-256 keys for ds-service.
//
// Usage:
//
//	go run services/ds-service/scripts/genkey.go
//
// Pipe the output into .env.local; values are base64-encoded.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

func main() {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fmt.Println("# JWT signing key (Ed25519)")
	fmt.Println("JWT_SIGNING_KEY=" + base64.StdEncoding.EncodeToString(priv))
	fmt.Println("JWT_PUBLIC_KEY=" + base64.StdEncoding.EncodeToString(pub))

	var enc [32]byte
	if _, err := rand.Read(enc[:]); err != nil {
		fmt.Println("# ERROR generating encryption key:", err)
		return
	}
	fmt.Println("")
	fmt.Println("# AES-256 encryption key (for Figma PATs at-rest)")
	fmt.Println("ENCRYPTION_KEY=" + base64.StdEncoding.EncodeToString(enc[:]))
}
