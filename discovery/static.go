package discovery

import (
	"github.com/daichitakahashi/lb/config"
	"github.com/rs/xid"
)

type static struct {
	l []*Backend
}

func Static(c []config.BackendConfig) Discoverer {
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
	return &static{
		l: l,
	}
}

func (s *static) Discover() ([]*Backend, error) {
	return s.l, nil
}
