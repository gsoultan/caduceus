// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package gss

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/jcmturner/gokrb5/v8/crypto"
	"github.com/jcmturner/gokrb5/v8/iana/keyusage"
	"github.com/jcmturner/gokrb5/v8/messages"
)

// RFC 4121's wrap token, section 4.2.6.2: a 16-byte header, and the
// plaintext, filler and a copy of the header, encrypted in the context's key.
const (
	wrapHeaderSize = 16
	// The header's flags.
	sentByAcceptor = 0x01
	sealed         = 0x02
	acceptorSubkey = 0x04
)

// ErrWrap is a message from the server that is not one of the context's: not
// encrypted, altered, sent the wrong way, or out of sequence.
var ErrWrap = errors.New("caduceus/gss: a message that is not the server's, as the context has it")

// establish keeps what protecting messages needs from the server's AP-REP:
// its subkey, which RFC 4121 says to use when it sends one, or else the
// ticket's session key; and its first sequence number.
func (k *Kerberos) establish(part messages.EncAPRepPart) {
	k.seal, k.acceptorSubkey = k.key, false
	if len(part.Subkey.KeyValue) > 0 {
		k.seal, k.acceptorSubkey = part.Subkey, true
	}
	k.recvSeq, k.recvSeqKnown = uint64(part.SequenceNumber), part.SequenceNumber != 0
	k.established = true
}

// header is a wrap token's header, the client's flags and its sequence
// number in it, and EC and RRC zero.
func (k *Kerberos) header(flags byte, seq uint64) []byte {
	if k.acceptorSubkey {
		flags |= acceptorSubkey
	}
	header := []byte{0x05, 0x04, flags, 0xff, 0, 0, 0, 0}
	return binary.BigEndian.AppendUint64(header, seq)
}

// Wrap encrypts plaintext for the server, as GSS_Wrap with confidentiality
// does: a header, then the plaintext and a copy of the header, encrypted with
// the initiator's sealing key usage. It needs no filler, since every
// encryption type Kerberos uses with RFC 4121 tokens takes data of any
// length.
func (k *Kerberos) Wrap(plaintext []byte) ([]byte, error) {
	if !k.established {
		return nil, errors.New("caduceus/gss: no context to wrap with: the server has not proved itself")
	}
	etype, err := crypto.GetEtype(k.seal.KeyType)
	if err != nil {
		return nil, fmt.Errorf("caduceus/gss: %w", err)
	}
	header := k.header(sealed, k.sendSeq)
	message := append(append(make([]byte, 0, len(plaintext)+wrapHeaderSize), plaintext...), header...)
	_, encrypted, err := etype.EncryptMessage(k.seal.KeyValue, message, keyusage.GSSAPI_INITIATOR_SEAL)
	if err != nil {
		return nil, fmt.Errorf("caduceus/gss: %w", err)
	}
	k.sendSeq++
	return append(header, encrypted...), nil
}

// Unwrap checks and decrypts a wrap token from the server: sent by the
// acceptor, encrypted, with the flags of this context, the next sequence
// number, and an encrypted copy of its header that matches the one sent in
// the clear. Its RRC, the bytes the sender rotated, is undone first.
func (k *Kerberos) Unwrap(token []byte) ([]byte, error) {
	if !k.established {
		return nil, errors.New("caduceus/gss: no context to unwrap with: the server has not proved itself")
	}
	if len(token) < wrapHeaderSize || token[0] != 0x05 || token[1] != 0x04 || token[3] != 0xff {
		return nil, fmt.Errorf("%w: not a wrap token", ErrWrap)
	}
	flags := token[2]
	if flags&sentByAcceptor == 0 || flags&sealed == 0 || flags&acceptorSubkey != 0 != k.acceptorSubkey {
		return nil, fmt.Errorf("%w: flags %#x", ErrWrap, flags)
	}
	filler := int(binary.BigEndian.Uint16(token[4:6]))
	seq := binary.BigEndian.Uint64(token[8:16])
	if k.recvSeqKnown && seq != k.recvSeq {
		return nil, fmt.Errorf("%w: sequence number %d, want %d", ErrWrap, seq, k.recvSeq)
	}
	plain, err := k.decrypt(token, filler)
	if err != nil {
		return nil, err
	}
	k.recvSeq, k.recvSeqKnown = seq+1, true
	return plain, nil
}

// decrypt decrypts a wrap token's body and checks the header copy in it.
func (k *Kerberos) decrypt(token []byte, filler int) ([]byte, error) {
	etype, err := crypto.GetEtype(k.seal.KeyType)
	if err != nil {
		return nil, fmt.Errorf("caduceus/gss: %w", err)
	}
	body := unrotate(token[wrapHeaderSize:], int(binary.BigEndian.Uint16(token[6:8])))
	// gokrb5 slices the checksum off without looking at the length first.
	if len(body) < etype.GetConfounderByteSize()+etype.GetHMACBitLength()/8+filler+wrapHeaderSize {
		return nil, fmt.Errorf("%w: %d bytes is too short", ErrWrap, len(token))
	}
	message, err := etype.DecryptMessage(k.seal.KeyValue, body, keyusage.GSSAPI_ACCEPTOR_SEAL)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrWrap, err)
	}
	if len(message) < filler+wrapHeaderSize {
		return nil, fmt.Errorf("%w: %d bytes decrypted", ErrWrap, len(message))
	}
	copied := message[len(message)-wrapHeaderSize:]
	clear := append([]byte(nil), token[:wrapHeaderSize]...)
	clear[6], clear[7] = 0, 0
	if !bytes.Equal(copied, clear) {
		return nil, fmt.Errorf("%w: the header was altered", ErrWrap)
	}
	return message[:len(message)-wrapHeaderSize-filler], nil
}

// unrotate undoes a rotation right by rrc bytes, which a sender such as
// Windows makes to put the checksum before the data.
func unrotate(body []byte, rrc int) []byte {
	if len(body) == 0 {
		return body
	}
	rrc %= len(body)
	return append(append(make([]byte, 0, len(body)), body[rrc:]...), body[:rrc]...)
}
