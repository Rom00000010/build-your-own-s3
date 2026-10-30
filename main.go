package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"build_your_own_s3/httpserver"
	"build_your_own_s3/objectstorage"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() (err error) {
	service, err := objectstorage.Open()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, service.Close()) }()

	handler := http.NewServeMux()
	uiHandler := http.StripPrefix("/_ui/", http.FileServer(http.Dir("web")))
	handler.Handle("GET /_ui/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		uiHandler.ServeHTTP(w, r)
	}))
	handler.Handle("/", httpserver.NewHandler(service))
	server := &http.Server{
		Addr:    "127.0.0.1:8080",
		Handler: handler,
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	log.Printf("listening on http://%s", listener.Addr())

	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()

	select {
	case err := <-finished:
		return err
	case <-signals:
		if err := server.Shutdown(context.Background()); err != nil {
			return err
		}
		if err := <-finished; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
