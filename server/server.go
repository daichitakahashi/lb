package server

import (
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"

	"github.com/daichitakahashi/lb/config"
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
	s := RoundRobinSelector{}

	return &http.Server{
		Addr: conf.Listen,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, err := s.Select(l, r)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, err.Error())
			}
			b.ServeHTTP(w, r)
		}),
	}, nil
}

type Backend struct {
	http.Handler
}

func newBackend(s config.BackendConfig) (*Backend, error) {
	u, err := url.Parse(s.URL)
	if err != nil {
		return nil, err
	}

	// TODO: ping

	p := httputil.NewSingleHostReverseProxy(u)
	return &Backend{
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

type Selector interface {
	Select(list *BackendList, req *http.Request) (*Backend, error)
}

type RoundRobinSelector struct {
	m   sync.Mutex
	cur uint64
}

func (r *RoundRobinSelector) Select(list *BackendList, _ *http.Request) (*Backend, error) {
	c := list.Candidates()
	l := uint64(len(c))
	if l == 0 {
		return nil, ErrBackendNotSelected
	}

	r.m.Lock()
	defer r.m.Unlock()
	b := c[r.cur%l]
	r.cur++
	return b, nil
}
