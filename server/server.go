package server

import (
	"errors"
	"io"
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
	s := []Selector{
		&CookieSelector{
			CookieName: "LB-Persistence",
		},
		&RoundRobinSelector{},
	}

	return &http.Server{
		Addr: conf.Listen,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var b *Backend
			var err error
			for _, ss := range s {
				b, err = ss.Select(l, w, r)
				if err == ErrBackendNotSelected {
					continue
				}
				break
			}
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, err.Error())
			}
			b.ServeHTTP(w, r)
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

type Selector interface {
	Select(list *BackendList, w http.ResponseWriter, r *http.Request) (*Backend, error)
}

type RoundRobinSelector struct {
	m   sync.Mutex
	cur uint64
}

func (s *RoundRobinSelector) Select(list *BackendList, _ http.ResponseWriter, _ *http.Request) (*Backend, error) {
	c := list.Candidates()
	l := uint64(len(c))
	if l == 0 {
		return nil, ErrBackendNotSelected
	}

	s.m.Lock()
	defer s.m.Unlock()
	b := c[s.cur%l]
	s.cur++
	return b, nil
}

type CookieSelector struct {
	CookieName string
}

func (s *CookieSelector) Select(list *BackendList, w http.ResponseWriter, r *http.Request) (*Backend, error) {
	c, err := r.Cookie(s.CookieName)
	if err != nil {
		return nil, ErrBackendNotSelected
	}

	// cookie validation?

	candidates := list.Candidates()
	for _, b := range candidates {
		if b.ID == c.Value {
			// Set-Cookie with new selected Backend's ID
			http.SetCookie(w, &http.Cookie{
				Name:  s.CookieName,
				Value: b.ID,
			})
			return b, nil
		}
	}

	// persisted backend is missing
	return nil, ErrBackendNotSelected
}
