package security

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"regexp"
	"time"
)

type TLSBundle struct {
	CA, Certificate, PrivateKey []byte
	CAFingerprint               string
}

func NewTLSBundle(hosts []string) (TLSBundle, error) {
	var out TLSBundle
	if len(hosts) == 0 {
		return out, errors.New("至少需要一个 TLS 主机名或 IP")
	}
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return out, err
	}
	serverPublic, serverPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return out, err
	}
	serial := func() (*big.Int, error) { return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128)) }
	caSerial, err := serial()
	if err != nil {
		return out, err
	}
	serverSerial, err := serial()
	if err != nil {
		return out, err
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: caSerial, Subject: pkix.Name{CommonName: "HopSend local pairing CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(1, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	leaf := &x509.Certificate{SerialNumber: serverSerial, Subject: pkix.Name{CommonName: "HopSend Agent"}, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(0, 0, 30), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	hostname := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]{0,252}$`)
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			leaf.IPAddresses = append(leaf.IPAddresses, ip)
		} else if hostname.MatchString(host) {
			leaf.DNSNames = append(leaf.DNSNames, host)
		} else {
			return out, errors.New("无效的 TLS 主机名或 IP")
		}
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, caPublic, caPrivate)
	if err != nil {
		return out, err
	}
	// Parse the issued CA so the automatically generated SubjectKeyId is present
	// when CreateCertificate derives the leaf's AuthorityKeyId (RFC 5280).
	issuer, err := x509.ParseCertificate(caDER)
	if err != nil {
		return out, err
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, issuer, serverPublic, caPrivate)
	if err != nil {
		return out, err
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(serverPrivate)
	if err != nil {
		return out, err
	}
	out.CA = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	out.Certificate = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	out.PrivateKey = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	hash := sha256.Sum256(caDER)
	out.CAFingerprint = hex.EncodeToString(hash[:])
	return out, nil
}
