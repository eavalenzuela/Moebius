package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"time"
)

// CA holds a loaded CA certificate and key for signing operations.
type CA struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
}

// LoadCA reads a PEM-encoded CA certificate and ECDSA private key from disk.
func LoadCA(certPath, keyPath string) (*CA, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read CA key: %w", err)
	}
	return ParseCA(certPEM, keyPEM)
}

// ParseCA parses PEM-encoded certificate and key bytes into a CA.
func ParseCA(certPEM, keyPEM []byte) (*CA, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("no PEM block found in CA certificate")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA certificate: %w", err)
	}
	if !cert.IsCA {
		return nil, fmt.Errorf("certificate is not a CA")
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("no PEM block found in CA key")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA private key: %w", err)
	}

	return &CA{Cert: cert, Key: key}, nil
}

// SignCSR signs an agent CSR with this CA, embedding the agentID in the
// DNS SAN and setting the given validity period.
func (ca *CA) SignCSR(csrPEM []byte, agentID string, validity time.Duration) (certPEM []byte, serialHex, fingerprint string, err error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil {
		return nil, "", "", fmt.Errorf("no PEM block found in CSR")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, "", "", fmt.Errorf("parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, "", "", fmt.Errorf("CSR signature invalid: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, "", "", err
	}

	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   agentID,
			Organization: []string{"Moebius Agent"},
		},
		DNSNames:              []string{agentID},
		NotBefore:             now,
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, ca.Cert, csr.PublicKey, ca.Key)
	if err != nil {
		return nil, "", "", fmt.Errorf("sign certificate: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	serialHex = hex.EncodeToString(serial.Bytes())
	fp := sha256.Sum256(certDER)
	fingerprint = hex.EncodeToString(fp[:])

	return certPEM, serialHex, fingerprint, nil
}

// CAChainPEM returns the PEM-encoded certificate chain (this CA's cert).
// If a root CA cert is also available, callers should concatenate it.
func (ca *CA) CAChainPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Cert.Raw})
}

// GenerateCA creates a new self-signed CA certificate and key pair.
// Use isRoot=true for a root CA (10yr, path constraint) or false for an
// intermediate CA (1yr). For intermediates, pass the parent CA to sign with;
// for root CAs, pass nil.
func GenerateCA(cn string, isRoot bool, parent *CA) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}

	now := time.Now().UTC()
	var validity time.Duration
	if isRoot {
		validity = 10 * 365 * 24 * time.Hour // ~10 years
	} else {
		validity = 365 * 24 * time.Hour // ~1 year
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   cn,
			Organization: []string{"Moebius"},
		},
		NotBefore:             now,
		NotAfter:              now.Add(validity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		MaxPathLen:            1,
	}
	if !isRoot {
		template.MaxPathLen = 0
		template.MaxPathLenZero = true
	}

	signerCert := template
	signerKey := key
	if parent != nil {
		signerCert = parent.Cert
		signerKey = parent.Key
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, signerCert, &key.PublicKey, signerKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create certificate: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal key: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

func randomSerial() (*big.Int, error) {
	serialMax := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialMax)
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}
	return serial, nil
}
