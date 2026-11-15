// storage-run is the storage container's supervisor, separate from the app.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/mikd1/tag-files/app/internal/deploymentinit"
	"github.com/mikd1/tag-files/app/internal/storageprofile"
)

func secret(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("required storage secret file unavailable: %s", path)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("storage secret must be a regular file: %s", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, 258))
	if err != nil || len(data) == 258 {
		return "", fmt.Errorf("invalid storage secret file: %s", path)
	}
	return strings.TrimSuffix(string(data), "\n"), nil
}
func run() error {
	if len(os.Args) >= 2 && os.Args[1] == "setup" {
		return deploymentinit.Run(os.Args[2:])
	}
	options := storageprofile.Options{DataDir: "/data", FilerConfig: "/etc/seaweedfs/filer.toml", S3Config: "/data/config/s3.json"}
	if len(os.Args) == 2 && os.Args[1] == "check" {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		return storageprofile.Ready(ctx, options)
	}
	if len(os.Args) != 1 {
		return fmt.Errorf("usage: storage-run [check | setup --directory PATH --version VERSION --app-image ID --storage-image ID]")
	}
	access, err := secret("/run/secrets/s3_access_key")
	if err != nil {
		return err
	}
	key, err := secret("/run/secrets/s3_secret_key")
	if err != nil {
		return err
	}
	u, err := user.Lookup("seaweed")
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	err = storageprofile.Run(ctx, options, aws.Credentials{AccessKeyID: access, SecretAccessKey: key}, uid, gid)
	if ctx.Err() != nil {
		return nil
	}
	return err
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
