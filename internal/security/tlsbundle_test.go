package security

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestTLSBundleHostnameTrustAndKeySeparation(t *testing.T) {
	bundle, err := NewTLSBundle([]string{"localhost", "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	rootBlock, _ := pem.Decode(bundle.CA)
	leafBlock, _ := pem.Decode(bundle.Certificate)
	root, err := x509.ParseCertificate(rootBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(leafBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	for _, name := range []string{"localhost", "127.0.0.1"} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: name}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "other-device.invalid"}); err == nil {
		t.Fatal("wrong hostname trusted")
	}
	if root.PublicKeyAlgorithm != x509.Ed25519 || leaf.IsCA {
		t.Fatal("unexpected certificate role")
	}
	if len(leaf.AuthorityKeyId) == 0 || !bytes.Equal(leaf.AuthorityKeyId, root.SubjectKeyId) {
		t.Fatal("leaf must identify its actual issuer for strict RFC 5280 clients")
	}
	if _, err := NewTLSBundle([]string{"https://localhost"}); err == nil {
		t.Fatal("URL accepted as SAN")
	}
}
