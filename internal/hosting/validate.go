package hosting

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"
)

var interfacePrefixes = realInterfacePrefixes

func realInterfacePrefixes() ([]netip.Prefix, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	var out []netip.Prefix
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.To4() == nil {
			continue
		}
		ones, bits := n.Mask.Size()
		if bits == 128 {
			ones -= 96
		}
		ip, _ := netip.AddrFromSlice(n.IP.To4())
		if p, err := ip.Prefix(ones); err == nil {
			out = append(out, p)
		}
	}
	return out, nil
}

func invalid(field string) error { return fmt.Errorf("%w: %s", ErrInvalid, field) }

// Validate checks a complete proposed configuration without side effects.
func Validate(c Config) error {
	if c.Relay != nil {
		if err := validateRelay(*c.Relay); err != nil {
			return err
		}
	}
	if len(c.Shares) > MaxShares {
		return invalid("too many shares")
	}
	ids := map[string]bool{}
	tuples := map[string]bool{}
	for _, s := range c.Shares {
		if err := validateShare(s); err != nil {
			return err
		}
		if ids[s.ID] {
			return invalid("duplicate share id")
		}
		ids[s.ID] = true
		for _, n := range []string{ProtoTCP, ProtoUDP} {
			if !s.covers(n) {
				continue
			}
			k := fmt.Sprintf("%s/%d", n, s.ExposedPort)
			if tuples[k] {
				return invalid("duplicate exposed port")
			}
			tuples[k] = true
		}
	}
	return nil
}

// ValidateCredential returns the canonical UUID in b, accepting trailing whitespace.
func ValidateCredential(b []byte) (string, error) {
	if len(b) > MaxCredential {
		return "", invalid("credential")
	}
	s := strings.TrimRight(string(b), " \t\r\n")
	if len(s) != 36 {
		return "", invalid("credential")
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return "", invalid("credential")
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return "", invalid("credential")
			}
		}
	}
	return s, nil
}

func validateRelay(r Relay) error {
	if r.Version != ProfileVersion {
		return invalid("relay version")
	}
	if !validLabel(r.Label) {
		return invalid("relay label")
	}
	if !validHost(r.Host) {
		return invalid("relay host")
	}
	if !validPort(r.Port) {
		return invalid("relay port")
	}
	if r.CredentialRef != "" && !validID(r.CredentialRef) {
		return invalid("credential reference")
	}
	if !validDNSName(r.Reality.ServerName) {
		return invalid("reality server name")
	}
	if !validPublicKey(r.Reality.PublicKey) {
		return invalid("reality public key")
	}
	if len(r.Reality.ShortID) == 0 || len(r.Reality.ShortID) > 16 || len(r.Reality.ShortID)%2 != 0 {
		return invalid("reality short id")
	}
	if _, err := hex.DecodeString(r.Reality.ShortID); err != nil {
		return invalid("reality short id")
	}
	return validVirtualAddress(r.Address)
}

func validVirtualAddress(s string) error {
	a, err := netip.ParseAddr(s)
	if err != nil || !a.Is4() || a.String() != s || !a.IsPrivate() {
		return invalid("virtual address")
	}
	prefixes, err := interfacePrefixes()
	if err != nil {
		return invalid("virtual address (interfaces unreadable)")
	}
	for _, p := range prefixes {
		if p.Contains(a) {
			return invalid("virtual address overlaps a local network")
		}
	}
	return nil
}

func validateShare(s Share) error {
	if !validID(s.ID) {
		return invalid("share id")
	}
	if !validLabel(s.Label) {
		return invalid("share label")
	}
	if s.LocalAddress != "127.0.0.1" && s.LocalAddress != "::1" {
		return invalid("share local address")
	}
	if !validPort(s.LocalPort) || !validPort(s.ExposedPort) {
		return invalid("share port")
	}
	switch s.Protocol {
	case ProtoTCP, ProtoUDP, ProtoBoth:
		return nil
	}
	return invalid("share protocol")
}

func validPort(p int) bool { return p >= 1 && p <= 65535 }

func validLabel(s string) bool {
	if len(s) == 0 || len(s) > MaxLabelBytes || !utf8.ValidString(s) || strings.TrimSpace(s) == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validID(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func validHost(s string) bool {
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Zone() == "" && !a.Is4In6() && !a.IsUnspecified() && !a.IsMulticast()
	}
	return validDNSName(s)
}

func validDNSName(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if len(l) == 0 || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	tld := strings.ToLower(labels[len(labels)-1])
	if tld == "local" || tld == "localhost" || tld == "arpa" {
		return false
	}
	numeric := true
	for i := 0; i < len(tld); i++ {
		if tld[i] < '0' || tld[i] > '9' {
			numeric = false
		}
	}
	return !numeric
}

func validPublicKey(s string) bool {
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 32 {
			return true
		}
	}
	return false
}
