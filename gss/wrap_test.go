// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package gss

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/jcmturner/gokrb5/v8/crypto"
	"github.com/jcmturner/gokrb5/v8/iana/etypeID"
	"github.com/jcmturner/gokrb5/v8/iana/keyusage"
	"github.com/jcmturner/gokrb5/v8/messages"
	"github.com/jcmturner/gokrb5/v8/types"
)

func established(t *testing.T, keyType int32, subkey bool, seq int64) (*Kerberos, types.EncryptionKey) {
	t.Helper()
	etype, err := crypto.GetEtype(keyType)
	if err != nil {
		t.Fatal(err)
	}
	key := types.EncryptionKey{KeyType: keyType, KeyValue: make([]byte, etype.GetKeyByteSize())}
	_, _ = rand.Read(key.KeyValue)
	k := &Kerberos{key: key, sendSeq: 7}
	part := messages.EncAPRepPart{SequenceNumber: seq}
	if subkey {
		part.Subkey = types.EncryptionKey{KeyType: keyType, KeyValue: make([]byte, etype.GetKeyByteSize())}
		_, _ = rand.Read(part.Subkey.KeyValue)
		key = part.Subkey
	}
	k.establish(part)
	return k, key
}

// acceptorToken is a wrap token as the server makes one: flags, filler,
// rotation and sequence number as given, sealed with the acceptor's usage.
func acceptorToken(t *testing.T, key types.EncryptionKey, flags byte, filler, rrc int, seq uint64, plain []byte) []byte {
	t.Helper()
	etype, _ := crypto.GetEtype(key.KeyType)
	header := []byte{0x05, 0x04, flags, 0xff, 0, byte(filler), 0, 0}
	header = binary.BigEndian.AppendUint64(header, seq)
	message := append(append(append([]byte(nil), plain...), make([]byte, filler)...), header...)
	_, sealed, err := etype.EncryptMessage(key.KeyValue, message, keyusage.GSSAPI_ACCEPTOR_SEAL)
	if err != nil {
		t.Fatal(err)
	}
	header[7] = byte(rrc)
	rrc %= len(sealed)
	rotated := append(append([]byte(nil), sealed[len(sealed)-rrc:]...), sealed[:len(sealed)-rrc]...)
	return append(header, rotated...)
}

// What Wrap makes is a sealed RFC 4121 token the server can open with the
// initiator's usage, its header copied inside, its sequence numbers counting
// up from the authenticator's.
func TestWrap(t *testing.T) {
	// Not aes256-cts-hmac-sha384-192: gokrb5 checks its derived encryption
	// key against the size of the base key and refuses to encrypt with it,
	// so a context of that type fails to wrap with an error. KDCs prefer
	// aes256-cts-hmac-sha1-96 by default.
	for _, keyType := range []int32{etypeID.AES256_CTS_HMAC_SHA1_96, etypeID.AES128_CTS_HMAC_SHA1_96, etypeID.AES128_CTS_HMAC_SHA256_128} {
		for _, subkey := range []bool{false, true} {
			k, key := established(t, keyType, subkey, 100)
			etype, _ := crypto.GetEtype(keyType)
			for i, plain := range [][]byte{[]byte("SELECT 1"), {}, bytes.Repeat([]byte{9}, 16000)} {
				token, err := k.Wrap(plain)
				if err != nil {
					t.Fatal(err)
				}
				wantFlags := byte(sealed)
				if subkey {
					wantFlags |= acceptorSubkey
				}
				if token[0] != 5 || token[1] != 4 || token[2] != wantFlags || token[3] != 0xff || binary.BigEndian.Uint64(token[8:16]) != uint64(7+i) {
					t.Fatalf("header %x", token[:16])
				}
				message, err := etype.DecryptMessage(key.KeyValue, token[16:], keyusage.GSSAPI_INITIATOR_SEAL)
				if err != nil {
					t.Fatalf("the server could not open it: %v", err)
				}
				if !bytes.Equal(message[:len(message)-16], plain) || !bytes.Equal(message[len(message)-16:], token[:16]) {
					t.Fatalf("sealed %x", message)
				}
			}
		}
	}
}

// Unwrap opens what the server sealed, rotated or not, with filler or not,
// and refuses what the server did not seal for this context.
func TestUnwrap(t *testing.T) {
	k, key := established(t, etypeID.AES256_CTS_HMAC_SHA1_96, true, 500)
	flags := byte(sentByAcceptor | sealed | acceptorSubkey)
	for i, tt := range []struct{ filler, rrc int }{{0, 0}, {0, 28}, {3, 0}, {2, 60}} {
		plain := []byte("result row")
		got, err := k.Unwrap(acceptorToken(t, key, flags, tt.filler, tt.rrc, uint64(500+i), plain))
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("filler %d rrc %d: %q, %v", tt.filler, tt.rrc, got, err)
		}
	}
	next := uint64(504)
	good := acceptorToken(t, key, flags, 0, 0, next, []byte("x"))
	altered := append([]byte(nil), good...)
	altered[len(altered)-1] ^= 1
	headerAltered := append([]byte(nil), good...)
	headerAltered[5] = 1 // filler the copy inside does not have
	refused := map[string][]byte{
		"from the client's side":  acceptorToken(t, key, sealed|acceptorSubkey, 0, 0, next, []byte("x")),
		"not sealed":              acceptorToken(t, key, sentByAcceptor|acceptorSubkey, 0, 0, next, []byte("x")),
		"without the subkey flag": acceptorToken(t, key, sentByAcceptor|sealed, 0, 0, next, []byte("x")),
		"replayed":                acceptorToken(t, key, flags, 0, 0, next-1, []byte("x")),
		"skipped ahead":           acceptorToken(t, key, flags, 0, 0, next+1, []byte("x")),
		"altered":                 altered,
		"header altered":          headerAltered,
		"short":                   good[:20],
		"only a header":           good[:16],
		"not a wrap token":        append([]byte{4, 4}, good[2:]...),
		"another key":             acceptorToken(t, types.EncryptionKey{KeyType: key.KeyType, KeyValue: make([]byte, 32)}, flags, 0, 0, next, []byte("x")),
	}
	for name, token := range refused {
		if _, err := k.Unwrap(token); !errors.Is(err, ErrWrap) {
			t.Errorf("%s: %v, want ErrWrap", name, err)
		}
	}
	if got, err := k.Unwrap(good); err != nil || string(got) != "x" {
		t.Errorf("after the refusals, the next token: %q, %v", got, err)
	}
}

// A context the server has not proved wraps and unwraps nothing.
func TestWrapBeforeEstablished(t *testing.T) {
	k := &Kerberos{}
	if _, err := k.Wrap([]byte("x")); err == nil {
		t.Error("wrapped before the context was established")
	}
	if _, err := k.Unwrap(make([]byte, 64)); err == nil {
		t.Error("unwrapped before the context was established")
	}
}

// The server's first sequence number is the one its reply named: a first
// token with another, a replay from some other connection, is refused.
func TestUnwrapFirstSequenceNumber(t *testing.T) {
	k, key := established(t, etypeID.AES256_CTS_HMAC_SHA1_96, true, 500)
	flags := byte(sentByAcceptor | sealed | acceptorSubkey)
	if _, err := k.Unwrap(acceptorToken(t, key, flags, 0, 0, 900, []byte("x"))); !errors.Is(err, ErrWrap) {
		t.Errorf("a first token numbered 900, not 500: %v, want ErrWrap", err)
	}
}
