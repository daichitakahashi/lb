package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"

	"github.com/daichitakahashi/lb/config"
	"github.com/rs/xid"
)

func NewServer(conf *config.Config) (*http.Server, error) {
	if len(conf.Backends) == 0 {
		return nil, errors.New("no backend")
	}

	backends := make([]*Backend, 0, len(conf.Backends))
	for _, s := range conf.Backends {
		b, err := newBackend(s)
		if err != nil {
			return nil, err
		}
		backends = append(backends, b)
	}
	l := &BackendList{
		backends: backends,
	}

	lb := make([]LoadBalancer, 0, 3)
	switch conf.PersistenceMethod {
	case "cookie":
		lb = append(lb, CookiePersistence("LB-Session"))
	}

	switch conf.Algorithm {
	case "round-robin":
		lb = append(lb, RoundRobin())
	default:
		return nil, errors.New("algorithm not specified")
	}

	lb = append(lb, base{})

	return &http.Server{
		Addr: conf.Listen,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := &Context{
				Context:  r.Context(),
				backends: l,
				lb:       lb,
				next:     0,
			}
			_, err := ctx.Next(w, r)
			if err != nil {
				status := http.StatusServiceUnavailable
				if err != ErrBackendNotSelected {
					status = http.StatusInternalServerError
				}
				w.WriteHeader(status)
				return
			}
		}),
	}, nil
}

type Backend struct {
	ID string
	http.Handler
}

func newBackend(s config.BackendConfig) (*Backend, error) {
	u, err := url.Parse(s.URL)
	if err != nil {
		return nil, err
	}

	if s.ID == "" {
		s.ID = xid.New().String()
	}

	// TODO: ping

	p := httputil.NewSingleHostReverseProxy(u)
	return &Backend{
		ID:      s.ID,
		Handler: p,
	}, nil
}

var ErrBackendNotSelected = errors.New("backend is not selected")

type BackendList struct {
	m        sync.RWMutex
	backends []*Backend
}

func (l *BackendList) Add(b *Backend) {
	l.m.Lock()
	defer l.m.Unlock()
	l.backends = append(l.backends, b)
}

func (l *BackendList) Remove(b *Backend) {
	l.m.Lock()
	defer l.m.Unlock()
	n := 0
	for _, bb := range l.backends {
		if bb == b {
			continue
		}
		l.backends[n] = bb
		n++
	}
	l.backends = l.backends[:n]
}

func (l *BackendList) Candidates() []*Backend {
	l.m.RLock()
	defer l.m.RUnlock()

	// return healthy backends
	return l.backends
}

type Context struct {
	context.Context
	backends *BackendList
	lb       []LoadBalancer
	next     int // next index of lb
}

func (c *Context) Candidates() []*Backend {
	return c.backends.Candidates()
}

func (c *Context) Proxy(b *Backend, w http.ResponseWriter, r *http.Request) {
	b.ServeHTTP(w, r)
}

func (c *Context) Next(w http.ResponseWriter, r *http.Request) (*Backend, error) {
	cc := *c
	lb := cc.lb[cc.next]
	cc.next++
	return lb.ServeBalancing(&cc, w, r)
}

type LoadBalancer interface {
	ServeBalancing(ctx *Context, w http.ResponseWriter, r *http.Request) (*Backend, error)
}

type base struct{}

func (b base) ServeBalancing(_ *Context, _ http.ResponseWriter, _ *http.Request) (*Backend, error) {
	return nil, ErrBackendNotSelected
}

type roundRobin struct {
	m   sync.Mutex
	cur uint64
}

func RoundRobin() LoadBalancer {
	return &roundRobin{}
}

func (s *roundRobin) ServeBalancing(ctx *Context, w http.ResponseWriter, r *http.Request) (*Backend, error) {
	c := ctx.Candidates()
	l := uint64(len(c))
	if l == 0 {
		return nil, ErrBackendNotSelected
	}

	s.m.Lock()
	b := c[s.cur%l]
	s.cur++
	s.m.Unlock()

	ctx.Proxy(b, w, r)
	return b, nil
}

type cookie struct {
	n string
}

func CookiePersistence(name string) LoadBalancer {
	return &cookie{
		n: name,
	}
}

func (s *cookie) ServeBalancing(ctx *Context, w http.ResponseWriter, r *http.Request) (*Backend, error) {
	c, err := r.Cookie(s.n)
	if err != nil {
		b, err := ctx.Next(w, r)
		if err != nil {
			return nil, err
		}

		// Set-Cookie with new selected Backend's ID
		http.SetCookie(w, &http.Cookie{
			Name:  s.n,
			Value: b.ID,
		})
		return b, nil
	}

	// cookie validation?

	candidates := ctx.Candidates()
	for _, b := range candidates {
		if b.ID == c.Value {
			ctx.Proxy(b, w, r)
			return b, nil
		}
	}

	// persisted backend is missing
	return ctx.Next(w, r)
}
