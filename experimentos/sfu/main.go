package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	secret := os.Getenv("KURZ_SECRET")
	if secret == "" {
		log.Fatal("KURZ_SECRET vazia")
	}
	port := 30100
	if v := os.Getenv("SFU_UDP_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			log.Fatalf("SFU_UDP_PORT inválida: %v", err)
		}
		port = p
	}
	// ponytail: só IPv4; IPv6 exigiria um segundo mux
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
	if err != nil {
		log.Fatal(err)
	}
	s, err := newServer([]byte(secret), udp, os.Getenv("SFU_PUBLIC_IP"),
		newTurnSource(os.Getenv("TURN_KEY_ID"), os.Getenv("TURN_KEY_API_TOKEN")))
	if err != nil {
		log.Fatal(err)
	}

	public := &http.Server{Addr: ":8081", Handler: s.publicMux(), ReadHeaderTimeout: 10 * time.Second}
	internal := &http.Server{Addr: ":8082", Handler: s.internalMux(), ReadHeaderTimeout: 10 * time.Second}
	for _, srv := range []*http.Server{public, internal} {
		go func() {
			if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				log.Fatal(err)
			}
		}()
	}
	log.Printf("sfu: ws :8081, interno :8082, udp %s", udp.LocalAddr())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	<-ctx.Done()
	log.Print("sfu: desligando")

	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = public.Shutdown(sctx) // fecha o listener; WebSockets sequestrados ficam com s.stop
	_ = internal.Shutdown(sctx)
	s.stop()
	_ = udp.Close()
}
