package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/daichitakahashi/lb/config"
	"github.com/rs/xid"
)

func NewServer(conf *config.Config) (*http.Server, error) {
	if len(conf.Backends) == 0 {
		return nil, errors.New("no backend")
	}

	t := http.DefaultTransport.(*http.Transport)
	t.MaxIdleConnsPerHost = 10

	backends := make([]*Backend, 0, len(conf.Backends))
	for _, s := range conf.Backends {
		b, err := newBackend(s, t.Clone())
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
	case "least-connection":
		lb = append(lb, LeastConnection())
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
			_, err := ctx.Next(w, r, nil)
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

type countConn struct {
	net.Conn
	count *uint64
}

func (c *countConn) Close() error {
	defer atomic.AddUint64(c.count, ^uint64(0)) // decrement
	// fmt.Println("close")
	return c.Conn.Close()
}

type Backend struct {
	*httputil.ReverseProxy
	ID    string
	conns uint64
}

func newBackend(s config.BackendConfig, t *http.Transport) (*Backend, error) {
	b := Backend{
		ID: s.ID,
	}
	if b.ID == "" {
		b.ID = xid.New().String()
	}

	// TODO: ping

	u, err := url.Parse(s.URL)
	if err != nil {
		return nil, err
	}
	b.ReverseProxy = httputil.NewSingleHostReverseProxy(u)
	dial := t.DialContext
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		added := atomic.AddUint64(&b.conns, 1)
		_ = added
		// fmt.Println(b.ID, "count:", added)

		return &countConn{
			Conn:  conn,
			count: &b.conns,
		}, nil
	}
	b.ReverseProxy.Transport = t

	d := b.ReverseProxy.Director
	b.ReverseProxy.Director = func(r *http.Request) {
		d(r)
		ctx := httptrace.WithClientTrace(r.Context(), &httptrace.ClientTrace{
			GetConn: nil,
			GotConn: func(info httptrace.GotConnInfo) {
				// fmt.Println("GotConn")
				// fmt.Println(info.Conn.RemoteAddr().String())
				// fmt.Printf("%#v\n", info)
				if info.Reused {
					// fmt.Println(b.ID, "reused")
				} else {
					// fmt.Println(b.ID, "new")
				}
			},
			PutIdleConn:      nil,
			ConnectStart:     nil,
			ConnectDone:      nil,
			WroteHeaderField: nil,
			WroteHeaders:     nil,
		})
		*r = *r.WithContext(ctx)
	}
	return &b, nil
}

func (b *Backend) Conns() uint64 {
	return atomic.LoadUint64(&b.conns)
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
	backends  *BackendList
	lb        []LoadBalancer
	next      int // next index of lb
	modifiers []ModifyResponse
}

func (c *Context) Candidates() []*Backend {
	// TODO: determine context candidates
	return c.backends.Candidates()
}

type ModifyResponse func(b *Backend, resp *http.Response) error

func (c *Context) modifyResponse(b *Backend) func(*http.Response) error {
	if len(c.modifiers) == 0 {
		return nil
	}
	return func(resp *http.Response) error {
		for _, m := range c.modifiers {
			err := m(b, resp)
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func (c *Context) Proxy(b *Backend, w http.ResponseWriter, r *http.Request, modify ModifyResponse) {
	if modify != nil {
		c.modifiers = append(c.modifiers, modify)
	}

	/*
		// Using sync.Mutex
		m := c.modifyResponse(b)
		b.m.Lock()
		b.ModifyResponse = func(resp *http.Response) error {
			defer b.m.Unlock()
			if m == nil {
				return nil
			}
			return m(resp)
		}
		b.ServeHTTP(w, r)
	*/

	// Copy httputil.ReverseProxy
	p := *b.ReverseProxy
	p.ModifyResponse = c.modifyResponse(b)
	p.ServeHTTP(w, r)
}

func (c *Context) Next(w http.ResponseWriter, r *http.Request, modify ModifyResponse) (*Backend, error) {
	cc := *c
	lb := cc.lb[cc.next]
	cc.next++
	if modify != nil {
		cc.modifiers = append(cc.modifiers, modify)
	}
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

func (s *roundRobin) next(list []*Backend) (int, *Backend, error) {
	l := uint64(len(list))
	if l == 0 {
		return 0, nil, ErrBackendNotSelected
	}

	s.m.Lock()
	idx := s.cur % l
	b := list[idx]
	s.cur++
	s.m.Unlock()
	return int(idx), b, nil
}

func (s *roundRobin) ServeBalancing(ctx *Context, w http.ResponseWriter, r *http.Request) (*Backend, error) {
	c := ctx.Candidates()
	_, b, err := s.next(c)
	if err != nil {
		return nil, err
	}
	ctx.Proxy(b, w, r, nil)
	return b, nil
}

type leastConnection struct {
	*roundRobin
}

func LeastConnection() LoadBalancer {
	return &leastConnection{
		roundRobin: &roundRobin{},
	}
}

func (s *leastConnection) ServeBalancing(ctx *Context, w http.ResponseWriter, r *http.Request) (*Backend, error) {
	c := ctx.Candidates()
	l := len(c)
	ni, n, err := s.roundRobin.next(c)
	if err != nil {
		return nil, err
	}

	minValue := n.Conns()
	for i := ni + 1; i%l != ni; i++ {
		b := c[i%l]
		if conns := b.Conns(); minValue > conns {
			n = b
			minValue = conns
		}
	}

	ctx.Proxy(n, w, r, nil)
	return n, nil
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
	if c, err := r.Cookie(s.n); err == nil {
		// cookie validation?

		candidates := ctx.Candidates()
		for _, b := range candidates {
			if b.ID == c.Value {
				ctx.Proxy(b, w, r, nil)
				return b, nil
			}
		}
	}

	// cookie not exists
	// or persisted backend is missing
	return ctx.Next(w, r, func(b *Backend, resp *http.Response) error {
		c := http.Cookie{
			Name:  s.n,
			Value: b.ID,
			Path:  "/",
		}
		resp.Header.Add("Set-Cookie", c.String())
		return nil
	})
}
