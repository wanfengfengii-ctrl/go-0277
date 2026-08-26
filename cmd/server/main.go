// Command server is the runnable entry point for the granary phosphine
// fumigation closure system. It opens the embedded database, runs the startup
// migration and recovery scan, then serves the HTTP API and the embedded
// frontend.
package main

import (
	"io/fs"
	"log"
	"net/http"
	"os"

	"granary-phosphine-fumigation-closure/internal/app"
	"granary-phosphine-fumigation-closure/internal/bboltstore"
	"granary-phosphine-fumigation-closure/internal/httpapi"
	"granary-phosphine-fumigation-closure/web"
)

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "granary.db"
	}

	db, err := bboltstore.Open(dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	report, err := db.Recover(nil)
	if err != nil {
		log.Fatalf("recovery scan failed: %v", err)
	}
	log.Printf("recovery: migrated=%v recovered=%d quarantined=%d",
		report.Migrated, len(report.RecoveredTasks), len(report.QuarantinedTasks))

	application := app.New(db, nil)

	assets, err := fs.Sub(webassets.Assets, "dist")
	if err != nil {
		log.Fatalf("embed frontend: %v", err)
	}

	api := httpapi.NewServer(application, assets)

	log.Printf("granary phosphine fumigation closure listening on %s", addr)
	if err := http.ListenAndServe(addr, api.Handler()); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
