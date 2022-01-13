package cmd

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/daichitakahashi/lb/config"
	"github.com/daichitakahashi/lb/server"
	"github.com/daichitakahashi/workerctl"
)

func Run(ctx context.Context, conf *config.Config) error {
	a := &workerctl.Aborter{}
	ctx = workerctl.WithAbort(ctx, a)

	ctl, shutdown := workerctl.New(ctx)

	a.AbortOnError(func() error {
		svr, err := server.NewServer(conf)
		if err != nil {
			return err
		}

		return ctl.Launch(workerctl.WorkerLauncherFunc(func(ctx context.Context) (stop func(ctx context.Context), err error) {
			go func() {
				err := workerctl.PanicSafe(func() error {
					return svr.ListenAndServe()
				})
				if err != nil && err != http.ErrServerClosed {
					log.Println(err)
					workerctl.Abort(ctx)
				}
			}()

			return func(ctx context.Context) {
				err := svr.Shutdown(ctx)
				if err != nil {
					log.Println(err)
				}
			}, nil
		}))
	}())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT)

	select {
	case <-a.Aborted():
		log.Println("aborted")
	case s := <-sig:
		log.Println("signal received:", s)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second*30)
	defer shutdownCancel()
	return shutdown(shutdownCtx)
}
