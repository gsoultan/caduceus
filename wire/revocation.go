// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// addRevocation makes config refuse a server whose certificate, or any
// certificate in the chain it sends, is listed in the revocation lists in
// crlFile or in the files of crlDir: libpq's sslcrl and sslcrldir. Each list
// is checked against its issuer's signature when the issuer is in the chain
// or in sslrootcert, and a list whose signature does not verify is refused.
// As in libpq, only a connection that verifies the server's certificate
// consults the lists.
func addRevocation(config *tls.Config, crlFile, crlDir, rootFile string) error {
	var lists []*x509.RevocationList
	if crlFile != "" {
		read, err := readRevocationLists(crlFile)
		if err != nil {
			return err
		}
		lists = append(lists, read...)
	}
	// A directory is read as OpenSSL reads one, for what lists it has: a file
	// in it that holds none, such as the CA certificate kept beside them, is
	// passed over.
	if crlDir != "" {
		entries, err := os.ReadDir(crlDir)
		if err != nil {
			return fmt.Errorf("%w: reading sslcrldir: %w", ErrInvalidConnString, err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			read, err := readRevocationLists(filepath.Join(crlDir, entry.Name()))
			if errors.Is(err, errNoList) {
				continue
			}
			if err != nil {
				return err
			}
			lists = append(lists, read...)
		}
	}
	if len(lists) == 0 && crlFile == "" {
		return nil
	}
	roots, err := rootCertificates(rootFile)
	if err != nil {
		return err
	}
	verify := config.VerifyConnection
	config.VerifyConnection = func(state tls.ConnectionState) error {
		if verify != nil {
			if err := verify(state); err != nil {
				return err
			}
		}
		return checkRevoked(state.PeerCertificates, roots, lists)
	}
	return nil
}

// errNoList is a file that holds no revocation list.
var errNoList = errors.New("no revocation list in the file")

// readRevocationLists reads every list in a file, PEM or DER.
func readRevocationLists(file string) ([]*x509.RevocationList, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("%w: reading sslcrl: %w", ErrInvalidConnString, err)
	}
	if !bytes.Contains(data, []byte("-----BEGIN")) {
		list, err := x509.ParseRevocationList(data)
		if err != nil {
			return nil, fmt.Errorf("%w: %w: %s: %w", ErrInvalidConnString, errNoList, file, err)
		}
		return []*x509.RevocationList{list}, nil
	}
	var lists []*x509.RevocationList
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "X509 CRL" {
			continue
		}
		list, err := x509.ParseRevocationList(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalidConnString, file, err)
		}
		lists = append(lists, list)
	}
	if len(lists) == 0 {
		return nil, fmt.Errorf("%w: %w: %s", ErrInvalidConnString, errNoList, file)
	}
	return lists, nil
}

// rootCertificates are the certificates in sslrootcert, the issuers a list
// may be signed by besides those in the chain the server sends.
func rootCertificates(rootFile string) ([]*x509.Certificate, error) {
	if rootFile == "" || rootFile == "system" {
		return nil, nil
	}
	data, err := os.ReadFile(rootFile)
	if err != nil {
		return nil, fmt.Errorf("%w: reading sslrootcert: %w", ErrInvalidConnString, err)
	}
	var roots []*x509.Certificate
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return roots, nil
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%w: sslrootcert: %w", ErrInvalidConnString, err)
		}
		roots = append(roots, certificate)
	}
}

// checkRevoked refuses a chain any certificate of which a list names.
func checkRevoked(chain, roots []*x509.Certificate, lists []*x509.RevocationList) error {
	issuers := append(append([]*x509.Certificate(nil), chain...), roots...)
	for _, certificate := range chain {
		for _, list := range lists {
			if !bytes.Equal(list.RawIssuer, certificate.RawIssuer) {
				continue
			}
			if err := checkListSignature(list, issuers); err != nil {
				return err
			}
			for _, entry := range list.RevokedCertificateEntries {
				if entry.SerialNumber.Cmp(certificate.SerialNumber) == 0 {
					return fmt.Errorf("%w: serial %s, issued by %s", ErrRevoked, certificate.SerialNumber, certificate.Issuer)
				}
			}
		}
	}
	return nil
}

// checkListSignature checks a list against the issuer it names, when that
// issuer is known.
func checkListSignature(list *x509.RevocationList, issuers []*x509.Certificate) error {
	for _, issuer := range issuers {
		if bytes.Equal(issuer.RawSubject, list.RawIssuer) {
			if err := list.CheckSignatureFrom(issuer); err != nil {
				return fmt.Errorf("caduceus/wire: a revocation list for %s is not signed by its issuer: %w", issuer.Subject, err)
			}
			return nil
		}
	}
	return nil
}
