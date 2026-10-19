package profile

import (
	"fmt"
	"strings"
	"testing"
)

func TestSubscriptionRejectsAmneziaUAPIInjection(t *testing.T) {
	for _, field := range []string{"h1", "h2", "h3", "h4", "i1", "i2", "i3", "i4", "i5"} {
		t.Run(field, func(t *testing.T) {
			value := "1\njc=1\njmin=-2\njmax=-1"
			if field[0] == 'i' {
				value = "<b 0x01>\njc=1\njmin=-2\njmax=-1"
			}
			body := fmt.Sprintf("proxies:\n  - {name: safe, type: direct}\n  - name: unsafe\n    type: wireguard\n    server: example.com\n    port: 51820\n    amnezia-wg-option:\n      %s: %q\n", field, value)
			p, dropped := build(t, body, nil)
			if len(p.Proxies) != 1 || p.Proxies[0].Name != "safe" || len(dropped) != 1 {
				t.Fatalf("subscription retained injected proxy: proxies=%v, diagnostics=%v", proxyNames(p), dropped)
			}
		})
	}
}

func TestSubscriptionAmneziaOptions(t *testing.T) {
	for _, tc := range []struct {
		name, options string
		valid         bool
	}{
		{"defaults", "{}", true},
		{"headers and tags", `{jc: 12, jmin: 40, jmax: 70, s1: 15, s2: 20, s3: 25, s4: 30, h1: "100-200", h2: "300", h3: "400", h4: "4294967295", i1: "<b 0XaBc><r 1000><rc 20><rd 20><t><c><wt 5000>", i2: "<b 0x01>"}`, true},
		{"bounds", `{jc: 128, jmin: 1280, jmax: 1280, s1: 1280, s2: 1280, s3: 1280, s4: 1280, i1: "<r 1000><r 280>"}`, true},
		{"empty fields", `{h1: "", i1: ""}`, true},
		{"numeric header", `{h1: 12345}`, true},
		{"reversed header", `{h1: "200-100"}`, false},
		{"header overflow", `{h1: "4294967296"}`, false},
		{"invalid header", `{h1: "one"}`, false},
		{"header CR", `{h1: "1\rjc=1"}`, false},
		{"header NUL", `{h1: "1\0"}`, false},
		{"missing tag name", `{i1: "< >"}`, false},
		{"unclosed tag", `{i1: "<r 20"}`, false},
		{"invalid tag", `{i1: "<wr>"}`, false},
		{"leading text", `{i1: "oops<b 0x01>"}`, false},
		{"trailing text", `{i1: "<b 0x01>oops"}`, false},
		{"invalid hex", `{i1: "<b 0xgg>"}`, false},
		{"missing hex prefix", `{i1: "<b 01>"}`, false},
		{"negative random", `{i1: "<r -1>"}`, false},
		{"negative chars", `{i1: "<rc -1>"}`, false},
		{"negative digits", `{i1: "<rd -1>"}`, false},
		{"random too large", `{i1: "<r 1001>"}`, false},
		{"packet too large", `{i1: "<r 1000><r 281>"}`, false},
		{"timestamp argument", `{i1: "<t 1>"}`, false},
		{"empty timestamp argument", `{i1: "<t >"}`, false},
		{"empty counter argument", `{i1: "<c >"}`, false},
		{"template tag count", `{i1: "` + strings.Repeat("<r 0>", 65) + `"}`, false},
		{"static packet size", `{i1: "<b 0x` + strings.Repeat("ff", 1281) + `>"}`, false},
		{"duplicate timestamp", `{i1: "<t><t>"}`, false},
		{"duplicate counter", `{i1: "<c><c>"}`, false},
		{"negative wait", `{i1: "<wt -1>"}`, false},
		{"wait too large", `{i1: "<wt 5001>"}`, false},
		{"cumulative waits", `{i1: "<wt 3000><wt 2001>"}`, false},
		{"waits across packets", `{i1: "<wt 3000>", i2: "<wt 2001>"}`, false},
		{"packet count", `{jc: 129}`, false},
		{"junk minimum", `{jmin: 1281, jmax: 1281}`, false},
		{"junk maximum", `{jmax: 1281}`, false},
		{"junk ordering", `{jmin: 10, jmax: 9}`, false},
		{"junk omitted maximum", `{jc: 1, jmin: 10}`, false},
		{"init padding", `{s1: 1281}`, false},
		{"response padding", `{s2: 1281}`, false},
		{"cookie padding", `{s3: 1281}`, false},
		{"transport padding", `{s4: 1281}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "proxies:\n  - {name: safe, type: direct}\n  - name: candidate\n    type: wireguard\n    server: example.com\n    port: 51820\n    amnezia-wg-option: " + tc.options + "\n"
			p, dropped := build(t, body, nil)
			want := 1
			if tc.valid {
				want = 2
			}
			if len(p.Proxies) != want || len(dropped) != 2-want {
				t.Fatalf("proxies=%v, diagnostics=%v, valid=%v", proxyNames(p), dropped, tc.valid)
			}
		})
	}
}
