// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// revocations are the certificate revocation lists sslcrl and sslcrldir
// name. As in libpq, whose OpenSSL checks every certificate of the chain
// against them, each certificate the server presents must be covered by a
// list its issuer signed, which is current and does not name it.
type revocations struct {
	lists []*x509.RevocationList
	now   func() time.Time
}

// loadRevocations reads sslcrl, a file of lists, and sslcrldir, a directory
// of them, in PEM or DER. It returns nil when neither is given.
func loadRevocations(file, dir string) (*revocations, error) {
	if file == "" && dir == "" {
		return nil, nil
	}
	r := &revocations{now: time.Now}
	if file != "" {
		if err := r.load(file); err != nil {
			return nil, err
		}
	}
	if dir != "" {
		if err := r.loadDir(dir); err != nil {
			return nil, err
		}
	}
	if len(r.lists) == 0 {
		return nil, fmt.Errorf("%w: sslcrl and sslcrldir hold no revocation list", ErrInvalidConnString)
	}
	return r, nil
}

// loadDir reads every regular file in dir that holds a list. A file that
// holds none, such as OpenSSL's hashed links to other files, is skipped.
func (r *revocations) loadDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("%w: reading sslcrldir: %w", ErrInvalidConnString, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		_ = r.load(filepath.Join(dir, entry.Name()))
	}
	return nil
}

// load reads the lists in one file.
func (r *revocations) load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: reading sslcrl: %w", ErrInvalidConnString, err)
	}
	ders := crlDERs(data)
	for _, der := range ders {
		list, err := x509.ParseRevocationList(der)
		if err != nil {
			return fmt.Errorf("%w: %s holds a revocation list that cannot be read: %w", ErrInvalidConnString, filepath.Base(path), err)
		}
		r.lists = append(r.lists, list)
	}
	if len(ders) == 0 {
		return fmt.Errorf("%w: %s holds no revocation list", ErrInvalidConnString, filepath.Base(path))
	}
	return nil
}

// crlDERs is each list in data: its PEM blocks, or data itself when it is
// not PEM, as a DER list is not.
func crlDERs(data []byte) [][]byte {
	if !bytes.Contains(data, []byte("-----BEGIN")) {
		return [][]byte{data}
	}
	var ders [][]byte
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return ders
		}
		if block.Type == "X509 CRL" {
			ders = append(ders, block.Bytes)
		}
	}
}

// check passes when one of chains, each from the server's certificate to a
// root, has no certificate revoked.
func (r *revocations) check(chains [][]*x509.Certificate) error {
	err := fmt.Errorf("%w: no chain to a trusted root", ErrRevoked)
	for _, chain := range chains {
		if err = r.checkChain(chain); err == nil {
			return nil
		}
	}
	return err
}

// checkChain checks every certificate below the root against the list its
// issuer, the next in the chain, signed.
func (r *revocations) checkChain(chain []*x509.Certificate) error {
	for i := 0; i+1 < len(chain); i++ {
		if err := r.checkOne(chain[i], chain[i+1]); err != nil {
			return err
		}
	}
	return nil
}

// checkOne checks a certificate against its issuer's list.
func (r *revocations) checkOne(certificate, issuer *x509.Certificate) error {
	list := r.issuedBy(issuer)
	if list == nil {
		return fmt.Errorf("%w: no revocation list from %s, which issued %s", ErrRevoked, issuer.Subject, certificate.Subject)
	}
	if !list.NextUpdate.IsZero() && r.now().After(list.NextUpdate) {
		return fmt.Errorf("%w: the revocation list from %s expired at %s", ErrRevoked, issuer.Subject, list.NextUpdate.Format(time.RFC3339))
	}
	if listed(list, certificate.SerialNumber) {
		return fmt.Errorf("%w: %s, serial %s", ErrRevoked, certificate.Subject, certificate.SerialNumber)
	}
	return nil
}

// issuedBy is the list issuer signed, or nil.
func (r *revocations) issuedBy(issuer *x509.Certificate) *x509.RevocationList {
	for _, list := range r.lists {
		if bytes.Equal(list.RawIssuer, issuer.RawSubject) && list.CheckSignatureFrom(issuer) == nil {
			return list
		}
	}
	return nil
}

// listed reports whether serial is on list.
func listed(list *x509.RevocationList, serial *big.Int) bool {
	for _, entry := range list.RevokedCertificateEntries {
		if entry.SerialNumber.Cmp(serial) == 0 {
			return true
		}
	}
	return false
}
