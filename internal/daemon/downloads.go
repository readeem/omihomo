package daemon

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/readeem/omihomo/internal/fetch"
	"github.com/readeem/omihomo/internal/protocol"
	"github.com/readeem/omihomo/internal/state"
)

type initialDownload struct{}

type pendingDownload struct {
	request  protocol.PendingDownload
	decision chan bool
}

func (d *server) downloadList() protocol.DownloadList {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := protocol.DownloadList{Downloads: []protocol.PendingDownload{}}
	for _, pending := range d.downloads {
		out.Downloads = append(out.Downloads, pending.request)
	}
	return out
}

func (d *server) publishDownloads(ctx context.Context) {
	d.pubMu.Lock()
	defer d.pubMu.Unlock()
	d.publishLocked(ctx, protocol.StreamDownloads, nil)
}

func (d *server) decideDownload(ctx context.Context, args protocol.DownloadDecisionArgs) (any, *protocol.Error) {
	d.mu.Lock()
	pending := d.downloads[args.ID]
	if pending != nil {
		delete(d.downloads, args.ID)
		pending.decision <- args.Allow
	}
	d.mu.Unlock()
	if pending == nil {
		return nil, fail(protocol.CodeNotFound, "this download is no longer waiting for approval")
	}
	d.publishDownloads(ctx)
	return protocol.Empty{}, nil
}

func (d *server) getDownload(ctx context.Context, label string, request fetch.Request) (*fetch.Response, error) {
	request.AllowDirect, _ = ctx.Value(initialDownload{}).(bool)
	response, err := d.fetcher.Get(ctx, request)
	if !errors.Is(err, fetch.ErrDirectRequired) {
		return response, err
	}
	approvalCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if len(label) > 512 {
		label = strings.ToValidUTF8(label[:512], "") + "…"
	}
	pending := &pendingDownload{
		request:  protocol.PendingDownload{ID: state.NewID(), Label: label},
		decision: make(chan bool, 1),
	}
	d.mu.Lock()
	if len(d.downloads) >= 64 {
		d.mu.Unlock()
		return nil, errors.New("too many downloads waiting for approval")
	}
	d.downloads[pending.request.ID] = pending
	d.mu.Unlock()
	d.publishDownloads(ctx)
	defer func() {
		d.mu.Lock()
		delete(d.downloads, pending.request.ID)
		d.mu.Unlock()
		d.publishDownloads(ctx)
	}()
	select {
	case <-approvalCtx.Done():
		return nil, errors.New("direct download approval expired")
	case allow := <-pending.decision:
		if !allow {
			return nil, errors.New("direct download declined")
		}
	}
	if approvalCtx.Err() != nil {
		return nil, errors.New("direct download approval expired")
	}
	// This permission belongs to one HTTP download, including its redirects.
	// Providers and later refreshes get independent requests and approvals.
	request.AllowDirect = true
	return d.fetcher.Get(ctx, request)
}
