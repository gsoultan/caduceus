// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package gss

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/gsoultan/caduceus/wire"
	"github.com/jcmturner/gokrb5/v8/client"
	"github.com/jcmturner/gokrb5/v8/config"
	"github.com/jcmturner/gokrb5/v8/credentials"
	"github.com/jcmturner/gokrb5/v8/crypto"
	"github.com/jcmturner/gokrb5/v8/gssapi"
	"github.com/jcmturner/gokrb5/v8/iana/flags"
	"github.com/jcmturner/gokrb5/v8/iana/keyusage"
	"github.com/jcmturner/gokrb5/v8/messages"
	"github.com/jcmturner/gokrb5/v8/spnego"
	"github.com/jcmturner/gokrb5/v8/types"
)

// ErrMutual is a server whose reply does not prove it holds the session key:
// not the server the ticket was for.
var ErrMutual = errors.New("caduceus/gss: the server's reply does not prove it is the server the ticket was for")

// Kerberos is one login's GSSAPI context, on a gokrb5 client.
type Kerberos struct {
	client *client.Client
	// key is the session key of the ticket sent, and ctime and cusec the
	// time of the authenticator sent with it, which the server's reply must
	// return encrypted in that key.
	key   types.EncryptionKey
	ctime time.Time
	cusec int
	// The established context, for encryption: the key it protects
	// messages with, the acceptor's subkey where the server sent one;
	// whether it is that subkey; and the sequence numbers of the next
	// message each way, the server's taken from its first where its reply
	// did not say.
	seal           types.EncryptionKey
	acceptorSubkey bool
	established    bool
	sendSeq        uint64
	recvSeq        uint64
	recvSeqKnown   bool
}

var _ wire.GSSWrapper = (*Kerberos)(nil)

// New starts a login from the credentials kinit left: the cache KRB5CCNAME
// names, or /tmp/krb5cc_<uid>, with the configuration in KRB5_CONFIG or
// /etc/krb5.conf. It is a wire.Config.NewGSS.
func New() (wire.GSS, error) {
	krb5conf, err := config.Load(configPath())
	if err != nil {
		return nil, fmt.Errorf("caduceus/gss: reading the Kerberos configuration: %w", err)
	}
	path, err := cachePath()
	if err != nil {
		return nil, err
	}
	cache, err := credentials.LoadCCache(path)
	if err != nil {
		return nil, fmt.Errorf("caduceus/gss: reading the credential cache %s (has kinit been run?): %w", path, err)
	}
	cl, err := client.NewFromCCache(cache, krb5conf, client.DisablePAFXFAST(true))
	if err != nil {
		return nil, fmt.Errorf("caduceus/gss: %w", err)
	}
	return &Kerberos{client: cl}, nil
}

// FromClient returns a wire.Config.NewGSS that logs in with cl, a gokrb5
// client built from a keytab or a password.
func FromClient(cl *client.Client) func() (wire.GSS, error) {
	return func() (wire.GSS, error) { return &Kerberos{client: cl}, nil }
}

func configPath() string {
	if path := os.Getenv("KRB5_CONFIG"); path != "" {
		return path
	}
	return "/etc/krb5.conf"
}

// cachePath is the file credential cache: KRB5CCNAME, with or without its
// FILE: prefix, or the default for the user. Other kinds of cache are not
// files gokrb5 can read.
func cachePath() (string, error) {
	if name := os.Getenv("KRB5CCNAME"); name != "" {
		kind, path, found := strings.Cut(name, ":")
		switch {
		case !found:
			return name, nil
		case kind == "FILE":
			return path, nil
		}
		return "", fmt.Errorf("caduceus/gss: KRB5CCNAME %q is a %s cache; only FILE caches can be read", name, kind)
	}
	current, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("caduceus/gss: %w", err)
	}
	return "/tmp/krb5cc_" + current.Uid, nil
}

// GetInitToken returns the first token for service/host, the host first
// canonicalised through DNS when the configuration says to, as MIT Kerberos
// does.
func (k *Kerberos) GetInitToken(host, service string) ([]byte, error) {
	if k.client.Config.LibDefaults.DNSCanonicalizeHostname {
		host = canonical(host)
	}
	return k.GetInitTokenFromSPN(service + "/" + host)
}

// canonical is host's canonical name, or host when it has none to give.
func canonical(host string) string {
	if net.ParseIP(host) != nil {
		return host
	}
	name, err := net.LookupCNAME(host)
	if err != nil || name == "" {
		return host
	}
	return strings.TrimSuffix(name, ".")
}

// GetInitTokenFromSPN returns the first token for the principal spn: a
// Kerberos AP-REQ asking the server to prove itself.
func (k *Kerberos) GetInitTokenFromSPN(spn string) ([]byte, error) {
	ticket, key, err := k.client.GetServiceTicket(spn)
	if err != nil {
		return nil, fmt.Errorf("caduceus/gss: getting a ticket for %s: %w", spn, err)
	}
	token, err := spnego.NewKRB5TokenAPREQ(k.client, ticket, key,
		[]int{gssapi.ContextFlagInteg, gssapi.ContextFlagConf, gssapi.ContextFlagMutual, gssapi.ContextFlagReplay, gssapi.ContextFlagSequence},
		[]int{flags.APOptionMutualRequired})
	if err != nil {
		return nil, fmt.Errorf("caduceus/gss: %w", err)
	}
	if err := k.remember(token.APReq, key); err != nil {
		return nil, err
	}
	return token.Marshal()
}

// remember keeps what the server's reply must prove it read: the session key,
// and the time of the authenticator.
func (k *Kerberos) remember(request messages.APReq, key types.EncryptionKey) error {
	plain, err := crypto.DecryptEncPart(request.EncryptedAuthenticator, key, keyusage.AP_REQ_AUTHENTICATOR)
	if err != nil {
		return fmt.Errorf("caduceus/gss: %w", err)
	}
	var authenticator types.Authenticator
	if err := authenticator.Unmarshal(plain); err != nil {
		return fmt.Errorf("caduceus/gss: %w", err)
	}
	k.key, k.ctime, k.cusec = key, authenticator.CTime, authenticator.Cusec
	k.sendSeq = uint64(authenticator.SeqNumber)
	return nil
}

// Continue checks the server's AP-REP: it must return the authenticator's
// time encrypted in the session key, which only the server the ticket was
// for can read. There is nothing to send back.
func (k *Kerberos) Continue(in []byte) (bool, []byte, error) {
	var token spnego.KRB5Token
	if err := token.Unmarshal(in); err != nil {
		return false, nil, fmt.Errorf("caduceus/gss: the server's token: %w", err)
	}
	if token.IsKRBError() {
		return false, nil, fmt.Errorf("caduceus/gss: the server refused the ticket: %s", token.KRBError.Error())
	}
	if !token.IsAPRep() {
		return false, nil, ErrMutual
	}
	plain, err := crypto.DecryptEncPart(token.APRep.EncPart, k.key, keyusage.AP_REP_ENCPART)
	if err != nil {
		return false, nil, fmt.Errorf("%w: %w", ErrMutual, err)
	}
	var part messages.EncAPRepPart
	if err := part.Unmarshal(plain); err != nil {
		return false, nil, fmt.Errorf("%w: %w", ErrMutual, err)
	}
	if !part.CTime.Equal(k.ctime) || part.Cusec != k.cusec {
		return false, nil, ErrMutual
	}
	k.establish(part)
	return true, nil, nil
}
