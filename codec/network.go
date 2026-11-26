// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

import (
	"math/big"
	"net"
	"strconv"
	"strings"
)

// Type OIDs of the network address types.
const (
	CIDR     uint32 = 650
	Inet     uint32 = 869
	Macaddr8 uint32 = 774
	Macaddr  uint32 = 829
)

// scanIPNet reads an inet or cidr into a *net.IPNet, keeping the address as
// written: inet '10.1.2.3/8' is 10.1.2.3 with an 8-bit mask, not 10.0.0.0, as
// pgx keeps it. An address with no mask has a full one.
func scanIPNet(oid uint32, format int16, src []byte, dest *net.IPNet) error {
	text, err := asString(oid, format, src)
	if err != nil {
		return err
	}
	if !strings.Contains(text, "/") {
		ip := net.ParseIP(text)
		if ip == nil {
			return errFormat(oid, "*net.IPNet")
		}
		bits := 8 * len(ip.To4())
		if bits == 0 {
			bits = 128
		}
		text += "/" + strconv.Itoa(bits)
	}
	ip, network, err := net.ParseCIDR(text)
	if err != nil {
		return errFormat(oid, "*net.IPNet")
	}
	if four := ip.To4(); four != nil {
		ip = four
	}
	*dest = net.IPNet{IP: ip, Mask: network.Mask}
	return nil
}

// scanHardwareAddr reads a macaddr or macaddr8 into a net.HardwareAddr. It
// used to be scanned as the bytes of its text, since a HardwareAddr is a
// []byte, which made a valid-looking address of the wrong length.
func scanHardwareAddr(oid uint32, format int16, src []byte, dest *net.HardwareAddr) error {
	if src == nil {
		*dest = nil
		return nil
	}
	text, err := asString(oid, format, src)
	if err != nil {
		return err
	}
	address, err := net.ParseMAC(text)
	if err != nil {
		return errFormat(oid, "net.HardwareAddr")
	}
	*dest = address
	return nil
}

// exactDecimal writes a *big.Rat as the decimal numeric reads, which its own
// MarshalText, 3/2, is not. A fraction with no finite decimal, such as 1/3,
// is refused rather than rounded.
func exactDecimal(dst []byte, r *big.Rat) ([]byte, error) {
	digits, ok := decimalDigits(r.Denom())
	if !ok {
		return dst, ErrInexact
	}
	return append(dst, r.FloatString(digits)...), nil
}

// decimalDigits is how many decimal places a fraction with this denominator
// needs, if it has a finite decimal: when the denominator is 2^a × 5^b, it is
// max(a, b).
func decimalDigits(denominator *big.Int) (int, bool) {
	rest := new(big.Int).Set(denominator)
	two, five, zero := big.NewInt(2), big.NewInt(5), new(big.Int)
	var twos, fives int
	modulus := new(big.Int)
	for rest.Cmp(big.NewInt(1)) != 0 {
		switch {
		case modulus.Mod(rest, two).Cmp(zero) == 0:
			rest.Quo(rest, two)
			twos++
		case modulus.Mod(rest, five).Cmp(zero) == 0:
			rest.Quo(rest, five)
			fives++
		default:
			return 0, false
		}
	}
	return max(twos, fives), true
}
