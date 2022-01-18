package server

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
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
		Listen:    "http://" + lb.Listener.Addr().String(),
		Algorithm: "round-robin",
		Backends:  backendConf,
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
		if resp.StatusCode != http.StatusOK {
			t.Fatal("status code is not 200: ", lb.URL)
		}
	}

	result := buf.String()
	if result != strings.Repeat(`server 0
server 1
server 2
`, 33) {
		t.Fatal("unexpected result:", result)
	}
}

type persistedClient struct {
	*http.Client
	content []byte
}

func (c *persistedClient) Do(u *url.URL) error {
	resp, err := c.Client.Get(u.String())
	if err != nil {
		return err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()

	if len(c.content) == 0 {
		c.content = body
	} else if !bytes.Equal(c.content, body) {
		return fmt.Errorf("response from different backend: expected: %s actual: %s", c.content, body)
	}
	return nil
}

func TestCookiePersistence(t *testing.T) {
	backends := make([]*httptest.Server, 5)
	backendConf := make([]config.BackendConfig, 5)
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
		Listen:            "http://" + lb.Listener.Addr().String(),
		Algorithm:         "round-robin",
		PersistenceMethod: "cookie",
		Backends:          backendConf,
	}
	svr, err := NewServer(&c)
	if err != nil {
		t.Fatal(err)
	}
	lb.Config = svr
	lb.Start()
	defer lb.Close()

	u, err := url.Parse(lb.URL)
	if err != nil {
		log.Fatal(err)
	}

	clients := make([]*persistedClient, 3)
	for i := range clients {
		jar, _ := cookiejar.New(nil)
		clients[i] = &persistedClient{
			Client: &http.Client{
				Jar: jar,
			},
			content: nil,
		}
	}

	for i := 0; i < 99; i++ {
		i := i % 3
		err := clients[i].Do(u)
		if err != nil {
			log.Fatal(err)
		}
	}

	if !bytes.Equal(clients[0].content, []byte("server 0")) ||
		!bytes.Equal(clients[1].content, []byte("server 1")) ||
		!bytes.Equal(clients[2].content, []byte("server 2")) {
		t.Fatal("unexpected responses")
	}

	for _, c := range clients {
		c.Client.Jar, _ = cookiejar.New(nil)
		c.content = nil
	}

	for i := 0; i < 99; i++ {
		i := i % 3
		err := clients[i].Do(u)
		if err != nil {
			log.Fatal(err)
		}
	}

	if !bytes.Equal(clients[0].content, []byte("server 3")) ||
		!bytes.Equal(clients[1].content, []byte("server 4")) ||
		!bytes.Equal(clients[2].content, []byte("server 0")) {
		t.Fatal("unexpected responses")
	}
}
