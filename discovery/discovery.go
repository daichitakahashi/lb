package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/daichitakahashi/lb/config"
	"github.com/rs/xid"
)

type Backend struct {
	ID  string
	URL string
}

type Discoverer interface {
	Discover() ([]*Backend, error)
}

type Fetcher interface {
	Fetch(ctx context.Context) ([]*Backend, error)
}

type discover struct {
	f Fetcher
	l []*Backend
	m sync.RWMutex
}

func New(ctx context.Context, f Fetcher, interval time.Duration) (Discoverer, func(), error) {
	var d discover
	ctx, cancel := context.WithCancel(ctx)

	l, err := f.Fetch(ctx)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	d.l = l

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
			l, err := f.Fetch(ctx)
			if err != nil {
				// TODO: エラーハンドリング
			}
			d.m.Lock()
			d.l = l
			d.m.Unlock()
		}
	}()
	return &d, cancel, nil
}

func (d *discover) Discover() ([]*Backend, error) {
	d.m.RLock()
	defer d.m.RUnlock()
	return d.l, nil
}

type jsonFetcher struct {
	url string
	c   *http.Client
}

func JSONDiscoverer(ctx context.Context, url string, interval time.Duration) (Discoverer, func(), error) {
	c := *http.DefaultClient // TODO: option
	return New(ctx, &jsonFetcher{
		url: url,
		c:   &c,
	}, interval)
}

func (f *jsonFetcher) Fetch(ctx context.Context) ([]*Backend, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	var c []config.BackendConfig
	err = json.NewDecoder(resp.Body).Decode(&c)
	if err != nil {
		return nil, err
	}

	l := make([]*Backend, len(c))
	for i := range c {
		id := c[i].ID
		if id == "" {
			id = xid.New().String()
		}
		l[i] = &Backend{
			ID:  id,
			URL: c[i].URL,
		}
	}
	return l, nil
}

type srvFetcher struct {
	r             *net.Resolver
	service, name string
}

func SRVDiscoverer(ctx context.Context, service, name string, interval time.Duration) (Discoverer, func(), error) {
	return New(ctx, &srvFetcher{
		r:       net.DefaultResolver, // option
		service: service,
		name:    name,
	}, interval)
}

func (f *srvFetcher) Fetch(ctx context.Context) ([]*Backend, error) {
	_, srv, err := f.r.LookupSRV(ctx, f.service, "tcp", f.name)
	if err != nil {
		return nil, err
	}

	l := make([]*Backend, len(srv))
	for i, s := range srv {
		// TODO: use srv.Priority, srv.Weight
		l[i] = &Backend{
			ID:  xid.New().String(),
			URL: fmt.Sprintf("%s:%d", s.Target, s.Port),
		}
	}
	return l, nil
}
