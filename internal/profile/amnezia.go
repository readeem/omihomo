package profile

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	// The legacy core allocates junk eagerly and sleeps for each wt tag.
	// Bound the whole handshake's work, not just individual tag arguments.
	maxAmneziaJunkCount  = 128
	maxAmneziaPacketSize = 1280
	maxAmneziaTags       = 64
	maxAmneziaWait       = 5000
)

func (p *amneziaOpts) check() error {
	for _, field := range []struct {
		name  string
		value *int
		max   int
	}{
		{"jc", p.JC, maxAmneziaJunkCount},
		{"jmin", p.JMin, maxAmneziaPacketSize}, {"jmax", p.JMax, maxAmneziaPacketSize},
		{"s1", p.S1, maxAmneziaPacketSize}, {"s2", p.S2, maxAmneziaPacketSize},
		{"s3", p.S3, maxAmneziaPacketSize}, {"s4", p.S4, maxAmneziaPacketSize},
	} {
		if field.value != nil && (*field.value < 0 || *field.value > field.max) {
			return fmt.Errorf("amnezia-wg-option.%s must be between 0 and %d", field.name, field.max)
		}
	}
	// mihomo omits absent and zero values; the legacy parser starts both at zero.
	if p.JMin != nil && *p.JMin > 0 && (p.JMax == nil || *p.JMax < *p.JMin) {
		return errors.New("amnezia-wg-option.jmin must not exceed jmax")
	}
	for i, header := range []*string{p.H1, p.H2, p.H3, p.H4} {
		if header != nil && *header != "" && !amneziaHeader(*header) {
			return fmt.Errorf("amnezia-wg-option.h%d must be a uint32 or an ordered uint32 range", i+1)
		}
	}
	wait := 0
	for i, packet := range []*string{p.I1, p.I2, p.I3, p.I4, p.I5} {
		if packet == nil || *packet == "" {
			continue
		}
		delay, err := amneziaPacket(*packet)
		if err != nil {
			return fmt.Errorf("amnezia-wg-option.i%d: %w", i+1, err)
		}
		wait += delay
	}
	if wait > maxAmneziaWait {
		return fmt.Errorf("amnezia-wg-option packet waits exceed %d milliseconds", maxAmneziaWait)
	}
	return nil
}

func amneziaHeader(s string) bool {
	lo, hi, ranged := strings.Cut(s, "-")
	if !ranged {
		hi = lo
	}
	a, err1 := strconv.ParseUint(lo, 10, 32)
	b, err2 := strconv.ParseUint(hi, 10, 32)
	return err1 == nil && err2 == nil && a <= b
}

func amneziaPacket(s string) (int, error) {
	// Keep parsing bounded even for a template made entirely of zero-sized tags.
	if len(s) > 2*maxAmneziaPacketSize+maxAmneziaTags*16 || strings.ContainsAny(s, "\r\n\x00") {
		return 0, errors.New("packet template is too long or contains control characters")
	}
	size, wait, tags := 0, 0, 0
	counter, timestamp := false, false
	for s != "" {
		if !strings.HasPrefix(s, "<") {
			return 0, errors.New("expected a packet tag")
		}
		tag, rest, ok := strings.Cut(s[1:], ">")
		if !ok {
			return 0, errors.New("unclosed packet tag")
		}
		name, param, _ := strings.Cut(tag, " ")
		param = strings.TrimSpace(param)
		n := 0
		switch name {
		case "b":
			if !strings.HasPrefix(param, "0x") && !strings.HasPrefix(param, "0X") {
				return 0, errors.New("byte tag needs hexadecimal bytes")
			}
			digits := param[2:]
			// The embedded parser accepts odd hex lengths by padding a leading zero.
			if len(digits)%2 != 0 {
				digits = "0" + digits
			}
			decoded, err := hex.DecodeString(digits)
			if err != nil {
				return 0, errors.New("byte tag needs hexadecimal bytes")
			}
			n = len(decoded)
		case "r", "rc", "rd", "wt":
			value, err := strconv.ParseUint(param, 10, 32)
			max := uint64(1000)
			if name == "wt" {
				max = maxAmneziaWait
			}
			if err != nil || value > max {
				return 0, errors.New("packet tag argument is out of range")
			}
			if name == "wt" {
				wait += int(value)
			} else {
				n = int(value)
			}
		case "c", "t":
			if tag != name || (name == "c" && counter) || (name == "t" && timestamp) {
				return 0, errors.New("counter and timestamp tags take no arguments and may appear once")
			}
			if name == "c" {
				counter = true
			} else {
				timestamp = true
			}
			n = 8
		default:
			return 0, errors.New("unsupported packet tag")
		}
		size += n
		tags++
		if size > maxAmneziaPacketSize || tags > maxAmneziaTags || wait > maxAmneziaWait {
			return 0, errors.New("packet template exceeds its size, tag or wait limit")
		}
		s = rest
	}
	return wait, nil
}
