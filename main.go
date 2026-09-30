// Marco watches the home network for family devices and texts the parents
// when one drops off the Wi-Fi during a scheduled window — which is what
// happens when a kid turns Wi-Fi off to dodge the router's parental controls.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // timezone names work even in minimal containers

	"marco/internal/monitor"
	"marco/internal/notify"
	"marco/internal/store"
	"marco/internal/web"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	addr := flag.String("addr", envOr("MARCO_ADDR", ":8080"), "HTTP listen address")
	dbPath := flag.String("db", envOr("MARCO_DB", "marco.db"), "path to the SQLite database")
	resetPassword := flag.String("reset-password", "", "set a new password for `username` and exit (reads the password from MARCO_NEW_PASSWORD)")
	flag.Parse()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer st.Close()

	if *resetPassword != "" {
		if err := resetAdminPassword(st, *resetPassword); err != nil {
			log.Fatal(err)
		}
		log.Printf("password updated for %s", *resetPassword)
		return
	}

	n := notify.New(st)
	mon := monitor.New(st, n)
	srv, err := web.New(st, mon, n)
	if err != nil {
		log.Fatalf("web: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go mon.Run(ctx)

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpSrv.Shutdown(shutdown)
	}()

	if os.Geteuid() != 0 {
		log.Printf("note: not running as root — sleeping phones are detected more reliably when marco can refresh the ARP table (run as root or with CAP_NET_ADMIN)")
	}
	log.Printf("marco listening on %s (database %s)", *addr, *dbPath)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func resetAdminPassword(st *store.Store, username string) error {
	pw := os.Getenv("MARCO_NEW_PASSWORD")
	if len(pw) < 8 {
		return errors.New("set MARCO_NEW_PASSWORD to the new password (at least 8 characters)")
	}
	a, err := st.GetAdminByUsername(username)
	if err != nil {
		return err
	}
	a.PasswordHash, err = store.HashPassword(pw)
	if err != nil {
		return err
	}
	return st.SaveAdmin(a)
}
