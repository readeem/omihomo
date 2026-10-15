// Package profile turns a downloaded subscription into the core.Profile the
// daemon runs. A subscription is remote content that ends up configuring a
// root process, so it is read into typed structs and only what they model
// survives: proxies of the modelled types and fields, select and automatic
// groups, rules of the known types, and providers, which are downloaded by
// the daemon and expanded here. Ports, listeners, DNS, TUN, the controller
// and every other setting belong to the daemon and are dropped.
package profile

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/dlclark/regexp2"
	"go.yaml.in/yaml/v3"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/fetch"
	"github.com/readeem/omihomo/internal/protocol"
)

// Links reads a list of share links into proxies as a subscription's YAML
// writes them. It is core.Core.ParseLinks.
type Links func(data []byte) ([]map[string]any, error)

// ErrInvalid is wrapped by every error that says the content is unusable,
// as opposed to the download failing.
var ErrInvalid = errors.New("the subscription is not usable")

// Kinds of provider, the first half of a Download's key.
const (
	ProxyProvider = "proxy"
	RuleProvider  = "rule"
)

// Download is a provider the document names, to be fetched before Build.
type Download struct {
	Kind string
	Name string
	URL  string
}

// Key identifies the download in the bodies Build takes.
func (d Download) Key() string { return d.Kind + ":" + d.Name }

// Document is a parsed subscription whose providers are not downloaded yet.
type Document struct {
	links          Links
	proxies        []map[string]any
	groups         []map[string]any
	rules          []string
	proxyProviders map[string]map[string]any
	ruleProviders  map[string]map[string]any
	// wrapped is a subscription of bare proxies: Build puts them in one
	// select group that every connection goes through.
	wrapped bool
	dropped []string
}

// The keys that make a YAML mapping a config rather than a list of links.
var structural = []string{"proxies", "proxy-providers", "proxy-groups", "rules", "rule-providers", "dns", "tun"}

type document struct {
	Proxies        []map[string]any          `yaml:"proxies"`
	Groups         []map[string]any          `yaml:"proxy-groups"`
	Rules          []string                  `yaml:"rules"`
	ProxyProviders map[string]map[string]any `yaml:"proxy-providers"`
	RuleProviders  map[string]map[string]any `yaml:"rule-providers"`
}

// Read parses a subscription body: a Clash YAML config, or share links,
// plain or base64, which links reads.
func Read(body []byte, links Links) (*Document, error) {
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	var top map[string]any
	if err := yaml.Unmarshal(body, &top); err == nil && slices.ContainsFunc(structural, func(k string) bool {
		_, ok := top[k]
		return ok
	}) {
		var doc document
		if err := yaml.Unmarshal(body, &doc); err != nil {
			return nil, fmt.Errorf("%w: the YAML does not read as a config: %v", ErrInvalid, yamlError(err))
		}
		d := &Document{links: links, proxies: doc.Proxies, groups: doc.Groups, rules: doc.Rules,
			proxyProviders: doc.ProxyProviders, ruleProviders: doc.RuleProviders}
		d.wrapped = len(d.groups) == 0 && len(d.rules) == 0
		return d, nil
	}
	proxies, err := links(body)
	if err != nil || len(proxies) == 0 {
		return nil, fmt.Errorf("%w: it is neither a config nor a list of share links", ErrInvalid)
	}
	return &Document{links: links, proxies: proxies, wrapped: true}, nil
}

// yamlError keeps the parser's reason without the content it quotes, which
// may hold credentials.
func yamlError(err error) string {
	var te *yaml.TypeError
	if errors.As(err, &te) {
		return fmt.Sprintf("%d values have the wrong type", len(te.Errors))
	}
	msg := err.Error()
	if line, _, ok := strings.Cut(msg, ":"); ok && strings.HasPrefix(msg, "yaml: line") {
		return line
	}
	return "malformed YAML"
}

// Downloads are the providers to fetch: those of type http with an HTTPS
// URL. The others are inline, or dropped by Build.
func (d *Document) Downloads() []Download {
	var out []Download
	add := func(kind string, providers map[string]map[string]any) {
		for _, name := range slices.Sorted(maps.Keys(providers)) {
			p := providers[name]
			url, _ := p["url"].(string)
			if p["type"] == "http" && fetch.CheckURL(url) == nil {
				out = append(out, Download{Kind: kind, Name: name, URL: url})
			}
		}
	}
	add(ProxyProvider, d.proxyProviders)
	add(RuleProvider, d.ruleProviders)
	return out
}

// Limits on what one subscription may hold.
const (
	maxName    = 256
	maxProxies = 20000
	maxGroups  = 2000
	maxRules   = 200000
)

// builtins are the targets mihomo always has.
var builtins = []string{"DIRECT", "REJECT", "REJECT-DROP", "PASS", "COMPATIBLE"}

// The group a wrapped subscription gets.
const wrapperGroup = "Proxy"

// Build makes the profile. bodies holds what was downloaded for each
// Download, by its Key; a provider without a body is dropped, along with
// what only it fed. dropped names what was left out, for the log; it never
// quotes credentials.
func (d *Document) Build(bodies map[string][]byte) (p core.Profile, dropped []string, err error) {
	b := &builder{doc: d, bodies: bodies, names: map[string]bool{}, dropped: slices.Clone(d.dropped)}
	for _, name := range builtins {
		b.names[name] = true
	}
	if len(d.proxies) > maxProxies || len(d.groups) > maxGroups || len(d.rules) > maxRules {
		return core.Profile{}, nil, fmt.Errorf("%w: it holds more proxies, groups or rules than omihomo runs", ErrInvalid)
	}
	for i, m := range d.proxies {
		px, err := Proxy(m)
		if err == nil {
			err = checkName(px.Name)
		}
		if err != nil {
			b.drop("proxy %d: %v", i+1, err)
			continue
		}
		if b.names[px.Name] {
			b.drop("proxy %q: the name is taken", px.Name)
			continue
		}
		b.names[px.Name] = true
		b.proxies = append(b.proxies, px)
	}
	b.topLevel = len(b.proxies)
	if err := b.providers(); err != nil {
		return core.Profile{}, nil, err
	}
	if d.wrapped {
		if len(b.proxies) == 0 {
			return core.Profile{}, nil, fmt.Errorf("%w: none of its proxies can run", ErrInvalid)
		}
		members := make([]string, len(b.proxies))
		for i, px := range b.proxies {
			members[i] = px.Name
		}
		if b.names[wrapperGroup] {
			return core.Profile{}, nil, fmt.Errorf("%w: a proxy is named %s", ErrInvalid, wrapperGroup)
		}
		p.Groups = []core.Group{{Name: wrapperGroup, Type: "select", Members: members, Options: map[string]any{}}}
		p.Rules = []core.Rule{{Type: "MATCH", Target: wrapperGroup, Source: protocol.SourceSubscription}}
		p.Proxies = b.proxies
		return p, b.dropped, nil
	}
	groups, err := b.groups()
	if err != nil {
		return core.Profile{}, nil, err
	}
	p.Proxies, p.Groups = b.proxies, groups
	p.RuleSets = b.ruleSets()
	p.Rules = b.rules(p.RuleSets)
	if len(p.Groups) == 0 && len(p.Proxies) == 0 {
		return core.Profile{}, nil, fmt.Errorf("%w: it has no proxies", ErrInvalid)
	}
	return p, b.dropped, nil
}

type builder struct {
	doc     *Document
	bodies  map[string][]byte
	names   map[string]bool
	dropped []string

	proxies []core.Proxy
	// topLevel counts the proxies the document lists itself; the provider
	// proxies follow them.
	topLevel int
	// fromProvider maps each provider to the names of its proxies.
	fromProvider map[string][]string
}

func (b *builder) drop(format string, args ...any) {
	b.dropped = append(b.dropped, fmt.Sprintf(format, args...))
}

func checkName(name string) error {
	switch {
	case name == "" || len(name) > maxName || strings.ContainsFunc(name, unicode.IsControl):
		return errors.New("the name is empty, too long or has control characters")
	case name != strings.TrimSpace(name) || strings.Contains(name, ","):
		return errors.New("the name has surrounding space or a comma")
	case name == "GLOBAL" || slices.Contains(builtins, name):
		return errors.New("the name is reserved")
	}
	return nil
}

// unique returns name, or name with the first free numeric suffix.
func (b *builder) unique(name string) string {
	candidate := name
	for i := 2; b.names[candidate]; i++ {
		candidate = name + " " + strconv.Itoa(i)
	}
	b.names[candidate] = true
	return candidate
}

// providers expands every proxy provider into proxies.
func (b *builder) providers() error {
	b.fromProvider = map[string][]string{}
	for _, name := range slices.Sorted(maps.Keys(b.doc.proxyProviders)) {
		def := b.doc.proxyProviders[name]
		var raw []map[string]any
		switch def["type"] {
		case "inline":
			list, ok := def["payload"].([]any)
			if !ok {
				b.drop("proxy provider %q: the payload is not a list", name)
				continue
			}
			for _, v := range list {
				if m, ok := v.(map[string]any); ok {
					raw = append(raw, m)
				}
			}
		case "http":
			body, ok := b.bodies[Download{Kind: ProxyProvider, Name: name}.Key()]
			if !ok {
				b.drop("proxy provider %q: not downloaded", name)
				continue
			}
			var err error
			if raw, err = b.providerProxies(body); err != nil {
				b.drop("proxy provider %q: %v", name, err)
				continue
			}
		default:
			b.drop("proxy provider %q: only http and inline providers are read", name)
			continue
		}
		f, err := newFilter(def)
		if err != nil {
			b.drop("proxy provider %q: %v", name, err)
			continue
		}
		prefix, suffix := "", ""
		if o, ok := def["override"].(map[string]any); ok {
			prefix, _ = o["additional-prefix"].(string)
			suffix, _ = o["additional-suffix"].(string)
		}
		names := []string{}
		for i, m := range raw {
			px, err := Proxy(m)
			if err == nil {
				px.Name = prefix + px.Name + suffix
				err = checkName(px.Name)
			}
			if err != nil {
				b.drop("proxy provider %q, proxy %d: %v", name, i+1, err)
				continue
			}
			if !f.keep(px) {
				continue
			}
			px.Name = b.unique(px.Name)
			b.proxies = append(b.proxies, px)
			names = append(names, px.Name)
		}
		b.fromProvider[name] = names
	}
	return nil
}

func (b *builder) providerProxies(body []byte) ([]map[string]any, error) {
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(body, &doc); err == nil && len(doc.Proxies) > 0 {
		return doc.Proxies, nil
	}
	proxies, err := b.doc.links(body)
	if err != nil || len(proxies) == 0 {
		return nil, errors.New("neither a proxy list nor share links")
	}
	return proxies, nil
}

// filter is mihomo's filter, exclude-filter and exclude-type, as a group or
// a provider writes them: regexes joined by backticks, and types joined by
// bars.
type filter struct {
	include, exclude []*regexp2.Regexp
	excludeTypes     []string
}

// A provider's or a group's filter runs on remote input, so it gets a
// bounded time per name.
const matchTimeout = 100 * time.Millisecond

func newFilter(def map[string]any) (filter, error) {
	var f filter
	compile := func(key string) ([]*regexp2.Regexp, error) {
		s, _ := def[key].(string)
		var out []*regexp2.Regexp
		for _, expr := range strings.Split(s, "`") {
			if expr == "" {
				continue
			}
			re, err := regexp2.Compile(expr, regexp2.None)
			if err != nil {
				return nil, fmt.Errorf("%s does not compile", key)
			}
			re.MatchTimeout = matchTimeout
			out = append(out, re)
		}
		return out, nil
	}
	var err error
	if f.include, err = compile("filter"); err != nil {
		return f, err
	}
	if f.exclude, err = compile("exclude-filter"); err != nil {
		return f, err
	}
	if s, _ := def["exclude-type"].(string); s != "" {
		for _, t := range strings.Split(s, "|") {
			f.excludeTypes = append(f.excludeTypes, strings.ToLower(strings.TrimSpace(t)))
		}
	}
	return f, nil
}

func (f filter) keep(px core.Proxy) bool {
	if slices.Contains(f.excludeTypes, strings.ToLower(px.Type)) {
		return false
	}
	match := func(res []*regexp2.Regexp) bool {
		for _, re := range res {
			if ok, err := re.MatchString(px.Name); ok && err == nil {
				return true
			}
		}
		return false
	}
	if len(f.include) > 0 && !match(f.include) {
		return false
	}
	return !match(f.exclude)
}

// groupTypes are the group types modelled. relay is deprecated in mihomo
// and left out.
var groupTypes = []string{"select", "url-test", "fallback", "load-balance"}

var strategies = []string{"consistent-hashing", "round-robin", "sticky-sessions"}

// groups reads the groups, expands what they take from providers, and drops
// members that do not exist. A group left without members fails the whole
// profile: dropping it would reroute the rules that send traffic to it.
func (b *builder) groups() ([]core.Group, error) {
	type pending struct {
		group   core.Group
		def     map[string]any
		members []any
	}
	var all []pending
	for i, def := range b.doc.groups {
		name, _ := def["name"].(string)
		typ, _ := def["type"].(string)
		if err := checkName(name); err != nil {
			b.drop("group %d: %v", i+1, err)
			continue
		}
		if !slices.Contains(groupTypes, typ) {
			b.drop("group %q: type %q is not supported", name, typ)
			continue
		}
		if b.names[name] {
			b.drop("group %q: the name is taken", name)
			continue
		}
		opts, err := groupOptions(typ, def)
		if err != nil {
			b.drop("group %q: %v", name, err)
			continue
		}
		b.names[name] = true
		members, _ := def["proxies"].([]any)
		all = append(all, pending{group: core.Group{Name: name, Type: typ, Options: opts}, def: def, members: members})
	}

	var out []core.Group
	for _, p := range all {
		g := p.group
		seen := map[string]bool{}
		add := func(name string) {
			if !seen[name] {
				seen[name] = true
				g.Members = append(g.Members, name)
			}
		}
		for _, v := range p.members {
			name, _ := v.(string)
			if name != "" && b.names[name] && name != g.Name {
				add(name)
			} else {
				b.drop("group %q: member %v does not exist", g.Name, v)
			}
		}
		f, err := newFilter(p.def)
		if err != nil {
			return nil, fmt.Errorf("%w: group %q: %v", ErrInvalid, g.Name, err)
		}
		includeAll := p.def["include-all"] == true
		if includeAll || p.def["include-all-proxies"] == true {
			for _, px := range b.proxies[:b.topLevel] {
				if f.keep(px) {
					add(px.Name)
				}
			}
		}
		var providers []string
		if includeAll || p.def["include-all-providers"] == true {
			providers = slices.Sorted(maps.Keys(b.fromProvider))
		} else if use, ok := p.def["use"].([]any); ok {
			for _, v := range use {
				if name, ok := v.(string); ok {
					providers = append(providers, name)
				}
			}
		}
		types := map[string]string{}
		for _, px := range b.proxies {
			types[px.Name] = px.Type
		}
		for _, provider := range providers {
			names, ok := b.fromProvider[provider]
			if !ok {
				b.drop("group %q: provider %q is not available", g.Name, provider)
				continue
			}
			for _, name := range names {
				if f.keep(core.Proxy{Name: name, Type: types[name]}) {
					add(name)
				}
			}
		}
		if len(g.Members) == 0 {
			return nil, fmt.Errorf("%w: group %q has no proxy omihomo can run", ErrInvalid, g.Name)
		}
		out = append(out, g)
	}
	return out, nil
}

// groupOptions keeps the options a group of typ may have.
func groupOptions(typ string, def map[string]any) (map[string]any, error) {
	opts := map[string]any{}
	if typ != "select" {
		if url, ok := def["url"].(string); ok {
			if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
				return nil, errors.New("the test URL is not http or https")
			}
			opts["url"] = url
		}
		for _, key := range []string{"interval", "timeout", "tolerance", "max-failed-times"} {
			if v, ok := def[key]; ok {
				n, err := wholeNumber(v)
				if err != nil {
					return nil, fmt.Errorf("%s: %v", key, err)
				}
				opts[key] = n
			}
		}
		if v, ok := def["lazy"].(bool); ok {
			opts["lazy"] = v
		}
		if s, ok := def["expected-status"].(string); ok {
			opts["expected-status"] = s
		} else if n, ok := def["expected-status"].(int); ok {
			opts["expected-status"] = strconv.Itoa(n)
		}
	}
	if typ == "load-balance" {
		if s, ok := def["strategy"].(string); ok {
			if !slices.Contains(strategies, s) {
				return nil, fmt.Errorf("strategy %q is not supported", s)
			}
			opts["strategy"] = s
		}
	}
	for _, key := range []string{"disable-udp", "hidden"} {
		if v, ok := def[key].(bool); ok {
			opts[key] = v
		}
	}
	return opts, nil
}

func wholeNumber(v any) (int, error) {
	switch v := v.(type) {
	case int:
		return v, nil
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n, nil
		}
	case float64:
		if v == float64(int(v)) {
			return int(v), nil
		}
	}
	return 0, errors.New("not a whole number")
}

var behaviors = []string{"domain", "ipcidr", "classical"}

// ruleSets reads every rule provider it can. mrs is mihomo's binary format,
// which it cannot check, and is dropped with the rules that use it.
func (b *builder) ruleSets() []core.RuleSet {
	var out []core.RuleSet
	for _, name := range slices.Sorted(maps.Keys(b.doc.ruleProviders)) {
		def := b.doc.ruleProviders[name]
		behavior, _ := def["behavior"].(string)
		format, _ := def["format"].(string)
		if !slices.Contains(behaviors, behavior) {
			b.drop("rule provider %q: behavior %q is not supported", name, behavior)
			continue
		}
		var payload []string
		var err error
		switch def["type"] {
		case "inline":
			payload, err = lines(def["payload"])
		case "http":
			body, ok := b.bodies[Download{Kind: RuleProvider, Name: name}.Key()]
			switch {
			case !ok:
				err = errors.New("not downloaded")
			case format == "" || format == "yaml":
				var doc struct {
					Payload any `yaml:"payload"`
				}
				if err = yaml.Unmarshal(body, &doc); err == nil {
					payload, err = lines(doc.Payload)
				}
			case format == "text":
				for line := range strings.Lines(string(body)) {
					line = strings.TrimSpace(line)
					if line != "" && !strings.HasPrefix(line, "#") {
						payload = append(payload, line)
					}
				}
			default:
				err = fmt.Errorf("format %q is not supported", format)
			}
		default:
			err = errors.New("only http and inline providers are read")
		}
		if err != nil {
			b.drop("rule provider %q: %v", name, err)
			continue
		}
		out = append(out, core.RuleSet{Name: name, Behavior: behavior, Payload: payload})
	}
	return out
}

func lines(v any) ([]string, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, errors.New("the payload is not a list")
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, errors.New("a payload line is not a string")
		}
		if s = strings.TrimSpace(s); s != "" && !strings.ContainsAny(s, "\r\n") {
			out = append(out, s)
		}
	}
	return out, nil
}

// rules keeps the rules of known types whose target and rule set exist.
func (b *builder) rules(sets []core.RuleSet) []core.Rule {
	var out []core.Rule
	for i, line := range b.doc.rules {
		r, err := ParseRule(line)
		switch {
		case err != nil:
			b.drop("rule %d: malformed", i+1)
			continue
		case !slices.Contains(ruleTypes, r.Type):
			b.drop("rule %d: type %s is not supported", i+1, r.Type)
			continue
		case !b.names[r.Target]:
			b.drop("rule %d: target %q does not exist", i+1, r.Target)
			continue
		case r.Type == "RULE-SET" && !slices.ContainsFunc(sets, func(s core.RuleSet) bool { return s.Name == r.Value }):
			b.drop("rule %d: rule set %q is not available", i+1, r.Value)
			continue
		}
		r.Params = slices.DeleteFunc(r.Params, func(p string) bool { return !slices.Contains(ruleParams, p) })
		r.Source = protocol.SourceSubscription
		out = append(out, r)
	}
	return out
}
