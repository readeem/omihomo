package daemon

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/protocol"
)

func (d *server) coreNameLocked() string {
	if d.cfg.forcedCore != "" {
		return d.cfg.forcedCore
	}
	if d.settings.Core == protocol.CoreXray && d.cfg.xray != nil {
		return protocol.CoreXray
	}
	return protocol.CoreMihomo
}

func (d *server) coreFor(name string) core.Core {
	if name == protocol.CoreXray {
		return d.cfg.xray
	}
	return d.core
}

func (d *server) activeCore() core.Core {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.coreFor(d.coreNameLocked())
}

type ruleKey struct {
	Type, Value, Target, Params, Source string
}

type compatibilityReporter interface {
	Compatibility(core.Config) core.Compatibility
}

func runtimeRuleKey(r protocol.Rule) ruleKey {
	return ruleKey{r.Type, r.Value, r.Target, strings.Join(strings.Fields(r.Params), " "), r.Source}
}

func (d *server) updateCoreCompatibility() {
	d.mu.Lock()
	c := d.coreFor(d.coreNameLocked())
	s, p := d.settings, d.profile
	d.mu.Unlock()

	var warnings []string
	var supported map[ruleKey]bool
	if reporter, ok := c.(compatibilityReporter); ok {
		report := reporter.Compatibility(runConfig(s, p))
		warnings = report.Warnings
		supported = make(map[ruleKey]bool, len(report.Rules))
		for _, r := range report.Rules {
			supported[runtimeRuleKey(r)] = true
		}
	}
	d.mu.Lock()
	d.coreWarnings, d.coreRules = warnings, supported
	d.mu.Unlock()
}

func (d *server) switchCore(ctx context.Context, args protocol.CoreSelectionArgs) (any, *protocol.Error) {
	name := args.Name
	if name != protocol.CoreMihomo && name != protocol.CoreXray {
		return nil, fail(protocol.CodeInvalid, "core must be mihomo or xray")
	}
	target := d.coreFor(name)
	if target == nil || d.cfg.forcedCore != "" && d.cfg.forcedCore != name {
		return nil, fail(protocol.CodeConflict, "%s is unavailable in this daemon", name)
	}

	d.opMu.Lock()
	defer d.opMu.Unlock()
	d.mu.Lock()
	previous := d.coreNameLocked()
	next, p := d.settings, d.profile
	d.mu.Unlock()
	if name == previous {
		return protocol.CoreSelection{Status: d.status()}, nil
	}

	cfg := runConfig(next, p)
	if reporter, ok := target.(compatibilityReporter); ok {
		warnings := reporter.Compatibility(cfg).Warnings
		if len(warnings) > 0 {
			mac := hmac.New(sha256.New, []byte(d.coreConfirmKey))
			mac.Write([]byte(name))
			if err := json.NewEncoder(mac).Encode(cfg); err != nil {
				return nil, fail(protocol.CodeInvalid, "could not confirm the active configuration")
			}
			token := hex.EncodeToString(mac.Sum(nil))
			if !hmac.Equal([]byte(args.Confirmation), []byte(token)) {
				return protocol.CoreSelection{Status: d.status(), ConfirmWarnings: warnings, Confirmation: token}, nil
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, coreError(err)
	}
	next.Core = name
	if !d.running() {
		if err := d.saveSettings(next); err != nil {
			return nil, err
		}
		d.updateCoreCompatibility()
		d.publish(ctx)
		return protocol.CoreSelection{Status: d.status()}, nil
	}
	if err := d.ensureGeodata(ctx, target, cfg.Profile); err != nil {
		return nil, fail(protocol.CodeFetch, "%v", err)
	}
	if err := target.Check(ctx, cfg); err != nil {
		return nil, coreError(err)
	}

	old := d.coreFor(previous)
	restore := func(failure *protocol.Error) (any, *protocol.Error) {
		// A client disconnect can cancel the switch after the previous core
		// released its listeners. Recovery still needs to run.
		restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := target.Stop(restoreCtx); err != nil {
			failure.Message += "; stop " + name + ": " + err.Error()
		}
		if err := old.Apply(restoreCtx, cfg); err != nil {
			failure.Message += "; restore " + previous + ": " + err.Error()
			d.setState(protocol.StateStopped, failure.Message)
		} else {
			d.setState(protocol.StateOn, "")
		}
		d.publish(restoreCtx)
		return nil, failure
	}

	d.setState(protocol.StateStopped, "")
	d.setState(protocol.StateStarting, "")
	d.publish(ctx)
	if err := old.Stop(ctx); err != nil {
		return restore(coreError(err))
	}
	if err := target.Apply(ctx, cfg); err != nil {
		return restore(coreError(err))
	}
	if err := d.saveSettings(next); err != nil {
		return restore(err)
	}
	d.updateCoreCompatibility()
	d.setState(protocol.StateOn, "")
	d.publish(ctx)
	return protocol.CoreSelection{Status: d.status()}, nil
}
