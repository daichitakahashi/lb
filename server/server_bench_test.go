package server

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/daichitakahashi/lb/config"
)

// Using sync.Mutex
// BenchmarkModifyResponse-8   	     392	   8461714 ns/op	  537657 B/op	    1806 allocs/op
// BenchmarkModifyResponse-8   	     570	   2671936 ns/op	  538092 B/op	    1807 allocs/op
// BenchmarkModifyResponse-8   	     588	   2362851 ns/op	  537487 B/op	    1806 allocs/op

// Copy httputil.ReverseProxy
// BenchmarkModifyResponse-8   	     765	   1837490 ns/op	  607457 B/op	    2174 allocs/op
// BenchmarkModifyResponse-8   	     813	   1795213 ns/op	  608017 B/op	    2176 allocs/op
// BenchmarkModifyResponse-8   	     772	   2648668 ns/op	  603552 B/op	    2161 allocs/op

func BenchmarkModifyResponse(b *testing.B) {
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// w.Header().Add("Connection", "Close")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("response"))
	}))
	defer be.Close()

	lb := httptest.NewUnstartedServer(nil)

	c := config.Config{
		Listen:    "http://" + lb.Listener.Addr().String(),
		Algorithm: "round-robin",
		Backends: []config.BackendConfig{
			{
				ID:  "backend-1",
				URL: be.URL,
			},
		},
	}
	svr, err := NewServer(&c)
	if err != nil {
		log.Fatalln(err)
	}
	lb.Config = svr
	lb.Start()
	defer lb.Close()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		for j := 0; j < 10; j++ {
			wg.Add(1)
			go func() {
				b.Helper()
				defer wg.Done()
				resp, err := http.Get(lb.URL)
				if err != nil {
					b.Error(err)
					return
				}
				body, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					b.Errorf("Response failed with status code: %d and\nbody: %s\n", resp.StatusCode, body)
					return
				} else if err != nil {
					b.Error(err)
					return
				}
			}()
		}
		wg.Wait()
	}
}
