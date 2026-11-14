// storage-check runs only inside the storage image; it is not an application
// command/API and never receives access to a user's application database.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/mikd1/tag-files/app/internal/storageprofile"
)

func main() {
	data := flag.String("data", "/data", "complete persistent storage root")
	filer := flag.String("filer-config", "/etc/seaweedfs/filer.toml", "pinned backend configuration")
	credentials := flag.String("s3-config", "/data/config/s3.json", "private S3 identity configuration")
	timeout := flag.Duration("timeout", 10*time.Second, "maximum preflight duration")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := storageprofile.Check(ctx, storageprofile.Options{DataDir: *data, FilerConfig: *filer, S3Config: *credentials}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
