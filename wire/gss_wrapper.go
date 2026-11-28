// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

// GSSWrapper is a GSS that can also protect traffic once its context is
// established: what a connection with gssencmode encrypts with, in place of
// TLS. It is its own interface, reached by type assertion, so that a GSS
// that only logs in, such as one written for pgx, still fits Config.NewGSS.
// The module github.com/gsoultan/caduceus/gss provides one.
type GSSWrapper interface {
	GSS
	// Wrap encrypts and signs plaintext for the server: GSS_Wrap with
	// confidentiality.
	Wrap(plaintext []byte) ([]byte, error)
	// Unwrap checks and decrypts a token from the server, and refuses one
	// that was not encrypted, was altered, or is out of sequence.
	Unwrap(token []byte) ([]byte, error)
}
