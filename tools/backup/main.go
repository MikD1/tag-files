package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "cold-backup:", e)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("use the external host cold-backup script: backup, verify or restore")
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	meta := f.String("metadata", "/metadata", "complete metadata root")
	storage := f.String("storage", "/storage", "complete storage root")
	deployment := f.String("deployment", "/deployment", "deployment/config/secrets root")
	dir := f.String("archive", "/archive", "archive destination or generation")
	gen := f.String("generation", "", "new generation name")
	ownerUID := f.Int("owner-uid", os.Getuid(), "archive owner UID")
	ownerGID := f.Int("owner-gid", os.Getgid(), "archive owner GID")
	minFree := f.Int64("min-free-bytes", 64<<20, "minimum free bytes after estimated complete generation and verification workspace")
	images := f.String("images", "", "JSON map of exact app, storage and tool image content IDs")
	if e := f.Parse(args[1:]); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	p := Paths{*meta, *storage, *deployment}
	var r Report
	var e error
	switch args[0] {
	case "backup":
		m := map[string]string{}
		if e = json.Unmarshal([]byte(*images), &m); e != nil {
			return e
		}
		r, e = Backup(Options{Paths: p, Destination: *dir, Generation: *gen, Images: m, OwnerUID: *ownerUID, OwnerGID: *ownerGID, SetOwner: true, MinFreeBytes: *minFree})
	case "verify":
		r, e = Verify(*dir)
	case "restore":
		r, e = Restore(*dir, p)
	default:
		return fmt.Errorf("unknown command")
	}
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(r)
}
