// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"hash"
	"strconv"
	"strings"
)

// maxSCRAMIterations bounds the key-stretching work a server may ask for.
// PostgreSQL's default is 4,096. The count comes from the peer before it has
// proved anything, so without a ceiling whoever answers on the server's address
// decides how long this process spends in PBKDF2.
const maxSCRAMIterations = 10_000_000

// scramNonceLen is the size of the client nonce, before base64.
const scramNonceLen = 18

// scramClient is the client half of SCRAM-SHA-256.
//
// The password is passed through SASLprep as PostgreSQL passes it when it is
// set (see saslprep), so that one SASLprep rewrites, such as one with
// full-width characters or a decomposed accent, authenticates.
type scramClient struct {
	password string
	// header is the GS2 header: n,, for no channel binding, y,, for a client
	// that could bind but was not offered PLUS, p=tls-server-end-point,, for
	// SCRAM-SHA-256-PLUS, whose binding is the hash of the server's
	// certificate.
	header  string
	binding []byte

	firstBare   string
	authMessage string
	serverKey   []byte
}

// first produces the client's opening message.
func (c *scramClient) first() ([]byte, error) {
	nonce := make([]byte, scramNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}

	// The username is empty on purpose: PostgreSQL takes it from the startup
	// packet and ignores this one.
	c.firstBare = "n=,r=" + base64.StdEncoding.EncodeToString(nonce)
	return []byte(c.header + c.firstBare), nil
}

// final consumes the server's challenge and produces the proof.
func (c *scramClient) final(serverFirst []byte) ([]byte, error) {
	text := string(serverFirst)

	nonce, saltText, countText, err := challengeFields(text)
	if err != nil {
		return nil, err
	}
	// A nonce that does not extend ours is a reply to somebody else's exchange.
	if !strings.HasPrefix(nonce, strings.TrimPrefix(c.firstBare, "n=,r=")) {
		return nil, ErrSCRAM
	}
	salt, err := base64.StdEncoding.DecodeString(saltText)
	if err != nil {
		return nil, ErrSCRAM
	}
	iterations, err := strconv.Atoi(countText)
	if err != nil || iterations < 1 || iterations > maxSCRAMIterations {
		return nil, ErrSCRAM
	}

	salted, err := pbkdf2.Key(sha256.New, saslprep(c.password), salt, iterations, sha256.Size)
	if err != nil {
		return nil, err
	}
	clientKey := sign(salted, "Client Key")
	storedKey := sha256.Sum256(clientKey)
	c.serverKey = sign(salted, "Server Key")

	// The channel-binding header, and the binding, repeated so the server can
	// tell if either was altered on the way: for n,, this is "biws".
	withoutProof := "c=" + base64.StdEncoding.EncodeToString(append([]byte(c.header), c.binding...)) + ",r=" + nonce
	c.authMessage = c.firstBare + "," + text + "," + withoutProof

	proof := sign(storedKey[:], c.authMessage)
	for i := range proof {
		proof[i] ^= clientKey[i]
	}
	return fmt.Appendf(nil, "%s,p=%s", withoutProof, base64.StdEncoding.EncodeToString(proof)), nil
}

// verify checks the server's signature. SCRAM is mutual: this is the step that
// proves the peer knew the password's verifier, and skipping it would let
// anything that can accept a connection stand in for the database.
func (c *scramClient) verify(serverFinal []byte) error {
	encoded, ok := strings.CutPrefix(string(serverFinal), "v=")
	if !ok || c.serverKey == nil {
		return ErrSCRAM
	}
	signature, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ErrSCRAM
	}
	if !hmac.Equal(signature, sign(c.serverKey, c.authMessage)) {
		return ErrSCRAM
	}
	return nil
}

// sign is HMAC-SHA-256.
func sign(key []byte, message string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	return mac.Sum(nil)
}

func challengeFields(text string) (nonce, salt, count string, err error) {
	for part := range strings.SplitSeq(text, ",") {
		switch {
		case strings.HasPrefix(part, "r="):
			nonce = part[2:]
		case strings.HasPrefix(part, "s="):
			salt = part[2:]
		case strings.HasPrefix(part, "i="):
			count = part[2:]
		}
	}
	if nonce == "" || salt == "" || count == "" {
		return "", "", "", ErrSCRAM
	}
	return nonce, salt, count, nil
}

// serverEndPoint is tls-server-end-point's channel binding: the hash of the
// server's certificate, with the hash its signature uses, and SHA-256 for
// MD5 and SHA-1, as RFC 5929 says. Nil without TLS, or for a certificate
// signed with neither, such as Ed25519, which has no such hash.
func (c *Conn) serverEndPoint() []byte {
	secured, ok := c.netConn.(*tls.Conn)
	if !ok {
		return nil
	}
	certificates := secured.ConnectionState().PeerCertificates
	if len(certificates) == 0 {
		return nil
	}
	return endPoint(certificates[0])
}

// endPoint hashes a certificate for tls-server-end-point.
func endPoint(certificate *x509.Certificate) []byte {
	var sum hash.Hash
	switch certificate.SignatureAlgorithm {
	case x509.MD5WithRSA, x509.SHA1WithRSA, x509.DSAWithSHA1, x509.ECDSAWithSHA1,
		x509.SHA256WithRSA, x509.SHA256WithRSAPSS, x509.DSAWithSHA256, x509.ECDSAWithSHA256:
		sum = sha256.New()
	case x509.SHA384WithRSA, x509.SHA384WithRSAPSS, x509.ECDSAWithSHA384:
		sum = sha512.New384()
	case x509.SHA512WithRSA, x509.SHA512WithRSAPSS, x509.ECDSAWithSHA512:
		sum = sha512.New()
	default:
		return nil
	}
	sum.Write(certificate.Raw)
	return sum.Sum(nil)
}
