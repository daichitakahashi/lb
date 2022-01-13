package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daichitakahashi/lb/config"
)

func TestRoundRobin(t *testing.T) {
	backends := make([]*httptest.Server, 3)
	backendConf := make([]config.BackendConfig, 3)
	for i := range backends {
		i := i
		s := httptest.NewServer(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, "server %d", i)
			}),
		)
		backends[i] = s
		backendConf[i] = config.BackendConfig{
			URL: s.URL,
		}
	}
	defer func() {
		for _, b := range backends {
			b.Close()
		}
	}()

	lb := httptest.NewUnstartedServer(nil)

	c := config.Config{
		Listen:   "http://" + lb.Listener.Addr().String(),
		Backends: backendConf,
	}
	svr, err := NewServer(&c)
	if err != nil {
		t.Fatal(err)
	}
	lb.Config = svr
	lb.Start()
	defer lb.Close()

	var buf strings.Builder
	for i := 0; i < 99; i++ {
		resp, err := http.Get(lb.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		buf.Write(body)
		buf.WriteRune('\n')
	}

	result := buf.String()
	if result != strings.Repeat(`server 0
server 1
server 2
`, 33) {
		t.Fatal("unexpected result:", result)
	}
}
