// Command briefbacktest replays the worker brief checks on past briefs.
//
// It reads a COPY of the gateway database, runs the text checks of
// protocol.CheckWorkerBrief on every brief sent to a worker, and joins each
// brief to what its worker did: fix rounds, early quits, the last run's
// outcome, ratings and defects found later. It never writes to the database.
// Overlap is not replayed because it needs live state.
//
// Make the copy first:
//
//	sqlite3 ~/.local/state/agent-net/gateway/gateway.db ".backup '<dir>/gw-copy.db'"
//
// Then run:
//
//	go run ./cmd/briefbacktest -db <dir>/gw-copy.db -csv <dir>/briefs.csv -report <dir>/report.md
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	dbPath := flag.String("db", "", "a copy of gateway.db; it is opened read-only")
	csvPath := flag.String("csv", "", "write one CSV row per brief to this file")
	reportPath := flag.String("report", "", "write the markdown report to this file (default: stdout)")
	flag.Parse()
	if *dbPath == "" {
		log.Fatal("-db is required: pass a copy of gateway.db")
	}

	db, err := sql.Open("sqlite", "file:"+*dbPath+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	res, err := runReplay(db)
	if err != nil {
		log.Fatal(err)
	}
	if *csvPath != "" {
		f, err := os.Create(*csvPath)
		if err != nil {
			log.Fatal(err)
		}
		if err := writeCSV(f, res.Briefs); err != nil {
			f.Close()
			log.Fatal(err)
		}
		if err := f.Close(); err != nil {
			log.Fatal(err)
		}
	}
	report := renderReport(res, *dbPath)
	if *reportPath == "" {
		fmt.Print(report)
		return
	}
	if err := os.WriteFile(*reportPath, []byte(report), 0o644); err != nil {
		log.Fatal(err)
	}
}
