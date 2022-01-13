package server

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync/atomic"

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

	h := &roundRobin{
		backends: backends,
		l:        uint64(len(backends)),
	}
	h.cur--

	return &http.Server{
		Addr:    conf.Listen,
		Handler: h,
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

type roundRobin struct {
	cur      uint64
	backends []*Backend
	l        uint64
}

func (rr *roundRobin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	i := atomic.AddUint64(&rr.cur, 1) % rr.l
	b := rr.backends[i]
	b.ServeHTTP(w, r)
}
