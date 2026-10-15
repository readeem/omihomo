package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/readeem/omihomo/internal/core"
	"github.com/readeem/omihomo/internal/fetch"
	"github.com/readeem/omihomo/internal/profile"
	"github.com/readeem/omihomo/internal/protocol"
	"github.com/readeem/omihomo/internal/state"
)

const (
	// maxProviders bounds the providers one subscription has downloaded.
	maxProviders = 64
	// manualGap is the least time between two requests to one
	// subscription's server that the owner asks for.
	manualGap = time.Minute
	// A refresh that gets no answer is retried after retryFirst, doubling
	// up to the subscription's interval. One the server refuses, or whose
	// content is unusable, waits a whole interval.
	retryFirst = time.Minute
	// geodataTimeout is the time a geodata file has to download; the files
	// run to tens of megabytes.
	geodataTimeout = 5 * time.Minute
	// geodataAge is when geodata in use is downloaded again.
	geodataAge = 7 * 24 * time.Hour
)

// activeSub is the active subscription's record, or nil. The caller holds
// mu, or is New.
func (d *server) activeSub() *state.Subscription {
	if d.subs.Active == "" {
		return nil
	}
	for i := range d.subs.List {
		if d.subs.List[i].ID == d.subs.Active {
			return &d.subs.List[i]
		}
	}
	return nil
}

func (d *server) findSub(name string) (state.Subscription, *protocol.Error) {
	if err := validName("subscription", name); err != nil {
		return state.Subscription{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, sub := range d.subs.List {
		if sub.Name == name {
			return sub, nil
		}
	}
	return state.Subscription{}, fail(protocol.CodeNotFound, "no subscription %q", name)
}

func ms(t time.Time) int64 { return t.UnixMilli() }

// nextUpdate is when the refresh loop next asks sub's server.
func nextUpdate(sub state.Subscription) int64 {
	if sub.RetryAt != 0 {
		return sub.RetryAt
	}
	return sub.UpdatedAt + sub.IntervalSeconds*1000
}

func (d *server) subscriptionList() protocol.SubscriptionList {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := protocol.SubscriptionList{Subscriptions: []protocol.Subscription{}}
	for _, sub := range d.subs.List {
		out.Subscriptions = append(out.Subscriptions, describe(sub, sub.ID == d.subs.Active))
	}
	return out
}

func describe(sub state.Subscription, active bool) protocol.Subscription {
	return protocol.Subscription{Name: sub.Name, UpdatedAt: sub.UpdatedAt, Upload: sub.Upload,
		Download: sub.Download, Total: sub.Total, Expire: sub.Expire, Active: active,
		Interval: sub.IntervalSeconds, NextUpdate: nextUpdate(sub), Error: sub.LastError}
}

// loadProfile builds sub's profile from what was last accepted for it.
func (d *server) loadProfile(sub state.Subscription) (core.Profile, error) {
	snap, err := d.store.Snapshot(sub.ID)
	if err != nil {
		return core.Profile{}, err
	}
	doc, err := profile.Read(snap.Body, d.core.ParseLinks)
	if err != nil {
		return core.Profile{}, err
	}
	p, _, err := doc.Build(snap.Providers)
	return p, err
}

// download is one accepted download of a subscription.
type download struct {
	snap    state.Snapshot
	header  http.Header
	profile core.Profile
}

// downloadError is a failed download, with how long the server asked to be
// left alone.
type downloadError struct {
	err *protocol.Error
	// answered is whether the server responded at all.
	answered   bool
	retryAfter time.Duration
}

func (d *server) download(ctx context.Context, name, url string) (*download, *downloadError) {
	resp, err := d.fetcher.Get(ctx, fetch.Request{URL: url})
	if err != nil {
		derr := &downloadError{err: fail(protocol.CodeFetch, "%v", err)}
		var serr *fetch.StatusError
		if errors.As(err, &serr) {
			derr.answered, derr.retryAfter = true, serr.RetryAfter
		}
		return nil, derr
	}
	rejected := func(err error) *downloadError {
		return &downloadError{err: fail(protocol.CodeRejected, "%v", err), answered: true}
	}
	doc, err := profile.Read(resp.Body, d.core.ParseLinks)
	if err != nil {
		return nil, rejected(err)
	}
	dl := &download{snap: state.Snapshot{Body: resp.Body, Providers: map[string][]byte{}}, header: resp.Header}
	providers := doc.Downloads()
	if len(providers) > maxProviders {
		return nil, rejected(fmt.Errorf("%w: it names more than %d providers", profile.ErrInvalid, maxProviders))
	}
	for _, p := range providers {
		r, err := d.fetcher.Get(ctx, fetch.Request{URL: p.URL})
		if err != nil {
			// Without it the rules it feeds would be left out, and with them
			// whatever they block: the content is incomplete, not new.
			derr := &downloadError{err: fail(protocol.CodeFetch, "provider %q: %v", p.Name, err)}
			var serr *fetch.StatusError
			if errors.As(err, &serr) {
				derr.answered, derr.retryAfter = true, serr.RetryAfter
			}
			return nil, derr
		}
		dl.snap.Providers[p.Key()] = r.Body
	}
	p, dropped, err := doc.Build(dl.snap.Providers)
	if err != nil {
		return nil, rejected(err)
	}
	for i, what := range dropped {
		if i == 20 {
			d.log.Warn("more was left out", "subscription", name, "count", len(dropped)-i)
			break
		}
		d.log.Warn("left out of the subscription", "subscription", name, "what", what)
	}
	dl.profile = p
	return dl, nil
}

func (d *server) addSubscription(ctx context.Context, url string) (any, *protocol.Error) {
	if err := fetch.CheckURL(url); err != nil {
		return nil, fail(protocol.CodeInvalid, "the subscription URL is not usable: %v", err)
	}
	d.refreshMu.Lock()
	defer d.refreshMu.Unlock()
	d.mu.Lock()
	taken := slices.ContainsFunc(d.subs.List, func(s state.Subscription) bool { return s.URL == url })
	d.mu.Unlock()
	if taken {
		return nil, fail(protocol.CodeConflict, "a subscription with this URL is already added")
	}
	dl, derr := d.download(ctx, "(new)", url)
	if derr != nil {
		return nil, derr.err
	}

	d.opMu.Lock()
	defer d.opMu.Unlock()
	now := d.cfg.now()
	usage := profile.ReadUsage(dl.header)
	sub := state.Subscription{ID: state.NewID(), URL: url, UpdatedAt: ms(now), LastAttempt: ms(now),
		IntervalSeconds: int64(profile.Interval(dl.header) / time.Second),
		Upload:          usage.Upload, Download: usage.Download, Total: usage.Total, Expire: usage.Expire}
	d.mu.Lock()
	next := d.subs
	sub.Name = uniqueName(next.List, profile.Name(dl.header, url))
	next.List = append(slices.Clone(next.List), sub)
	// The first subscription is the one to use: nothing runs without one.
	activate := next.Active == ""
	if activate {
		next.Active = sub.ID
	}
	settings := d.settings
	d.mu.Unlock()

	if err := d.acceptContent(ctx, settings, dl.profile, activate); err != nil {
		return nil, err
	}
	if err := d.store.SaveSnapshot(sub.ID, dl.snap); err != nil {
		return nil, fail(protocol.CodeInternal, "the subscription could not be saved: %v", err)
	}
	if err := d.saveSubs(next); err != nil {
		d.store.RemoveSnapshot(sub.ID)
		return nil, err
	}
	if activate {
		d.mu.Lock()
		d.profile = dl.profile
		d.mu.Unlock()
	}
	d.log.Info("subscription added", "subscription", sub.Name, "active", activate)
	d.publish(ctx)
	return describe(sub, activate), nil
}

// uniqueName is name, or name with the first free numeric suffix.
func uniqueName(subs []state.Subscription, name string) string {
	candidate := name
	for i := 2; slices.ContainsFunc(subs, func(s state.Subscription) bool { return s.Name == candidate }); i++ {
		candidate = fmt.Sprintf("%s %d", name, i)
	}
	return candidate
}

func (d *server) saveSubs(next state.Subscriptions) *protocol.Error {
	if err := d.store.SaveSubscriptions(next); err != nil {
		d.log.Error("save the subscriptions", "err", err)
		return fail(protocol.CodeInternal, "the subscriptions could not be saved: %v", err)
	}
	d.mu.Lock()
	d.subs = next
	d.mu.Unlock()
	return nil
}

// updateSub saves the record of sub changed by edit, if it still exists.
// The caller holds opMu.
func (d *server) updateSub(id string, edit func(*state.Subscription)) *protocol.Error {
	d.mu.Lock()
	next := d.subs
	next.List = slices.Clone(next.List)
	i := slices.IndexFunc(next.List, func(s state.Subscription) bool { return s.ID == id })
	d.mu.Unlock()
	if i < 0 {
		return fail(protocol.CodeNotFound, "the subscription was removed meanwhile")
	}
	edit(&next.List[i])
	return d.saveSubs(next)
}

func (d *server) updateSubscription(ctx context.Context, name string) (any, *protocol.Error) {
	sub, err := d.findSub(name)
	if err != nil {
		return nil, err
	}
	return d.refresh(ctx, sub.ID, true)
}

// mayAsk says why the owner may not ask sub's server now, or nil.
func mayAsk(sub state.Subscription, now time.Time) *protocol.Error {
	if wait := time.UnixMilli(sub.LastAttempt).Add(manualGap).Sub(now); wait > 0 {
		return fail(protocol.CodeConflict, "%s was asked %s ago; try again in %s",
			sub.Name, now.Sub(time.UnixMilli(sub.LastAttempt)).Round(time.Second), wait.Round(time.Second))
	}
	if sub.HeldUntil > ms(now) {
		return fail(protocol.CodeConflict, "the server of %s asked to wait until %s",
			sub.Name, time.UnixMilli(sub.HeldUntil).Format(time.DateTime))
	}
	return nil
}

// refresh downloads subscription id again and, when it works, makes it the
// subscription's content, running it if it is active and the proxy runs.
// Whatever fails leaves the content that worked before in place. The
// deadlines are read under refreshMu, so a request queued behind another
// sees what that one recorded: a manual one is refused, and a scheduled one
// that is no longer due does nothing.
func (d *server) refresh(ctx context.Context, id string, manual bool) (any, *protocol.Error) {
	d.refreshMu.Lock()
	defer d.refreshMu.Unlock()
	d.mu.Lock()
	i := slices.IndexFunc(d.subs.List, func(s state.Subscription) bool { return s.ID == id })
	var sub state.Subscription
	if i >= 0 {
		sub = d.subs.List[i]
	}
	d.mu.Unlock()
	if i < 0 {
		return nil, fail(protocol.CodeNotFound, "the subscription was removed meanwhile")
	}
	if manual {
		if err := mayAsk(sub, d.cfg.now()); err != nil {
			return nil, err
		}
	} else if nextUpdate(sub) > ms(d.cfg.now()) {
		return nil, nil
	}

	dl, derr := d.download(ctx, sub.Name, sub.URL)
	d.opMu.Lock()
	defer d.opMu.Unlock()
	now := d.cfg.now()
	interval := time.Duration(sub.IntervalSeconds) * time.Second
	if derr != nil {
		d.log.Warn("subscription refresh failed", "subscription", sub.Name, "err", derr.err.Message)
		d.failRefresh(sub, now, interval, derr.answered, derr.retryAfter, derr.err.Message)
		d.publish(ctx)
		return nil, derr.err
	}

	d.mu.Lock()
	active := d.subs.Active == id
	settings := d.settings
	exists := slices.ContainsFunc(d.subs.List, func(s state.Subscription) bool { return s.ID == id })
	d.mu.Unlock()
	if !exists {
		return nil, fail(protocol.CodeNotFound, "the subscription was removed meanwhile")
	}
	if err := d.acceptContent(ctx, settings, dl.profile, active); err != nil {
		// Its interval still counts: the server answered.
		interval = profile.Interval(dl.header)
		d.failRefresh(sub, now, interval, true, 0, err.Message)
		d.publish(ctx)
		return nil, err
	}
	if err := d.store.SaveSnapshot(id, dl.snap); err != nil {
		return nil, fail(protocol.CodeInternal, "the subscription could not be saved: %v", err)
	}
	usage := profile.ReadUsage(dl.header)
	if err := d.updateSub(id, func(s *state.Subscription) {
		s.UpdatedAt, s.LastAttempt, s.RetryAt, s.HeldUntil, s.LastError = ms(now), ms(now), 0, 0, ""
		s.IntervalSeconds = int64(profile.Interval(dl.header) / time.Second)
		s.Upload, s.Download, s.Total, s.Expire = usage.Upload, usage.Download, usage.Total, usage.Expire
		sub = *s
	}); err != nil {
		return nil, err
	}
	if active {
		d.mu.Lock()
		d.profile = dl.profile
		d.mu.Unlock()
	}
	d.log.Info("subscription refreshed", "subscription", sub.Name)
	d.publish(ctx)
	return describe(sub, active), nil
}

// failRefresh records a failed request and when to try again. The caller
// holds opMu.
func (d *server) failRefresh(sub state.Subscription, now time.Time, interval time.Duration, answered bool, retryAfter time.Duration, why string) {
	wait := interval
	if !answered {
		// Nothing answered, as when the network is not up yet: try again
		// soon, then less and less often.
		wait = retryFirst
		if sub.RetryAt > sub.LastAttempt && sub.LastAttempt > 0 {
			wait = 2 * time.Duration(sub.RetryAt-sub.LastAttempt) * time.Millisecond
		}
		wait = min(wait, interval)
	}
	wait = max(wait, retryAfter)
	if err := d.updateSub(sub.ID, func(s *state.Subscription) {
		s.LastAttempt, s.RetryAt, s.LastError = ms(now), ms(now.Add(wait)), why
		if retryAfter > 0 {
			s.HeldUntil = ms(now.Add(retryAfter))
		}
	}); err != nil {
		d.log.Error("record a failed refresh", "subscription", sub.Name, "err", err.Message)
	}
}

func (d *server) activateSubscription(ctx context.Context, name string) (any, *protocol.Error) {
	// Under opMu, so the subscription cannot be removed in between.
	d.opMu.Lock()
	defer d.opMu.Unlock()
	sub, perr := d.findSub(name)
	if perr != nil {
		return nil, perr
	}
	d.mu.Lock()
	next, settings := d.subs, d.settings
	d.mu.Unlock()
	if next.Active == sub.ID {
		return d.status(), nil
	}
	p, err := d.loadProfile(sub)
	if err != nil {
		return nil, fail(protocol.CodeRejected, "%s cannot be read: %v", sub.Name, err)
	}
	if err := d.acceptContent(ctx, settings, p, true); err != nil {
		return nil, err
	}
	next.Active = sub.ID
	if err := d.saveSubs(next); err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.profile = p
	if d.state == protocol.StateStopped {
		d.detail = ""
	}
	d.mu.Unlock()
	d.log.Info("subscription activated", "subscription", sub.Name)
	d.publish(ctx)
	return d.status(), nil
}

func (d *server) removeSubscription(ctx context.Context, name string) (any, *protocol.Error) {
	sub, err := d.findSub(name)
	if err != nil {
		return nil, err
	}
	d.opMu.Lock()
	defer d.opMu.Unlock()
	d.mu.Lock()
	next := d.subs
	d.mu.Unlock()
	active := next.Active == sub.ID
	if active && d.running() {
		return nil, fail(protocol.CodeConflict, "%s is running; stop the proxy or activate another subscription first", sub.Name)
	}
	next.List = slices.DeleteFunc(slices.Clone(next.List), func(s state.Subscription) bool { return s.ID == sub.ID })
	if active {
		next.Active = ""
	}
	if err := d.saveSubs(next); err != nil {
		return nil, err
	}
	if active {
		d.mu.Lock()
		d.profile = core.Profile{}
		d.mu.Unlock()
	}
	if err := d.store.RemoveSnapshot(sub.ID); err != nil {
		d.log.Warn("remove a subscription's content", "subscription", sub.Name, "err", err)
	}
	d.log.Info("subscription removed", "subscription", sub.Name)
	d.publish(ctx)
	return protocol.Empty{}, nil
}

// ensureGeodata downloads the geodata files p needs that are missing. A
// file that cannot be downloaded fails the call: the rules that need it
// would not load.
func (d *server) ensureGeodata(ctx context.Context, p core.Profile) error {
	for _, f := range d.core.Geodata(p) {
		if _, err := os.Stat(f.Path); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("geodata %s: %w", filepath.Base(f.Path), err)
		}
		if err := d.fetchGeodata(ctx, f); err != nil {
			return err
		}
	}
	return nil
}

func (d *server) fetchGeodata(ctx context.Context, f core.GeodataFile) error {
	name := filepath.Base(f.Path)
	d.log.Info("downloading geodata", "file", name)
	resp, err := d.fetcher.Get(ctx, fetch.Request{URL: f.URL, Timeout: geodataTimeout})
	if err != nil {
		return fmt.Errorf("the rules need geodata %s, which did not download: %w", name, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.Path), "."+name+"-*")
	if err != nil {
		return fmt.Errorf("save geodata %s: %w", name, err)
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(resp.Body)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), f.Path)
	}
	if err != nil {
		return fmt.Errorf("save geodata %s: %w", name, err)
	}
	return nil
}

// refreshGeodata downloads again the geodata in use that is older than
// geodataAge. The core reads the new files on its next apply.
func (d *server) refreshGeodata(ctx context.Context) {
	d.mu.Lock()
	p := runConfig(d.settings, d.profile).Profile
	d.mu.Unlock()
	for _, f := range d.core.Geodata(p) {
		info, err := os.Stat(f.Path)
		if err != nil || d.cfg.now().Sub(info.ModTime()) < geodataAge {
			continue
		}
		if err := d.fetchGeodata(ctx, f); err != nil {
			d.log.Warn("geodata update failed", "err", err)
		}
	}
}

// retryRestore turns the proxy back on if it was on before the daemon
// stopped and has not come up since. It runs what was last accepted: a
// refresh needs the network, which may not be up yet.
func (d *server) retryRestore(ctx context.Context) {
	d.opMu.Lock()
	defer d.opMu.Unlock()
	d.mu.Lock()
	pending := d.restorePending
	d.mu.Unlock()
	if !pending {
		return
	}
	if err := d.startLocked(ctx); err != nil {
		d.log.Warn("the proxy was on before the daemon stopped and does not start yet", "err", err.Message)
		d.mu.Lock()
		d.restorePending = true
		d.mu.Unlock()
	}
}

// refreshLoop restores the proxy, refreshes subscriptions as they fall
// due, and keeps geodata current.
func (d *server) refreshLoop(ctx context.Context) {
	t := time.NewTicker(d.cfg.refreshEvery)
	defer t.Stop()
	for {
		d.retryRestore(ctx)
		d.refreshDue(ctx)
		d.refreshGeodata(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (d *server) refreshDue(ctx context.Context) {
	d.mu.Lock()
	var due []string
	now := ms(d.cfg.now())
	for _, sub := range d.subs.List {
		if nextUpdate(sub) <= now {
			due = append(due, sub.ID)
		}
	}
	d.mu.Unlock()
	for _, id := range due {
		if ctx.Err() != nil {
			return
		}
		// A refresh records its own failure and retry time.
		d.refresh(ctx, id, false)
	}
}
