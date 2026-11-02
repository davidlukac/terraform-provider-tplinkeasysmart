// Command configbackup saves a switch's configuration file through the ESCP client library, the same
// code the provider uses. It replaces nothing the provider ships: it is a maintenance tool for the
// scripts that take a backup before a change and compare the file's hash afterwards.
//
//	configbackup -out FILE [-host ADDRESS] [-mac AA:BB:CC:DD:EE:FF]
//
// The switch is named by -host or -mac (or TPLINK_EASYSMART_HOST / TPLINK_EASYSMART_MAC); with a MAC
// the library pins the identity and finds the switch when the address is stale. The credentials come
// from TPLINK_EASYSMART_USERNAME and TPLINK_EASYSMART_PASSWORD and are never taken from the command
// line, so they cannot show up in a process list.
//
// The file is written only once ConfigBackup has returned a complete, verified download (see
// escp.Client.ConfigBackup): a lost packet makes the library download again, and when it cannot get the
// whole file no file is written at all and the exit code is 1. FILE must not exist; it is created
// with mode 0600 because the file holds the obfuscated account block of the switch. On success one
// line is printed: "FILE SIZE bytes sha256=HEX".
//
// Exit codes: 0 saved, 1 the backup failed, 2 the command line or environment is wrong.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/davidlukac/escp-go"
)

// Environment variables, the provider's own names.
const (
	envHost     = "TPLINK_EASYSMART_HOST"
	envMAC      = "TPLINK_EASYSMART_MAC"
	envUsername = "TPLINK_EASYSMART_USERNAME"
	envPassword = "TPLINK_EASYSMART_PASSWORD" //nolint:gosec // the name of an environment variable, not a credential
)

// The exit codes.
const (
	exitOK      = 0
	exitFailed  = 1
	exitUsage   = 2
	backupLimit = 2 * time.Minute // the whole backup, all rounds included
)

// deps are the two things a test replaces: escp.Dial (aimed at a fake switch) and the creation of the
// output file (a writer that fails).
type deps struct {
	dial   func(escp.Config) (*escp.Client, error)
	create func(path string) (io.WriteCloser, error)
}

// say writes a line of output. The write to a terminal or pipe can fail, and then there is nobody to tell.
func say(w io.Writer, format string, args ...any) { _, _ = fmt.Fprintf(w, format, args...) }

// createExclusive creates path, which must not exist, with mode 0600.
func createExclusive(path string) (io.WriteCloser, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the path is the operator's own -out argument
}

// run is the command: it returns the exit code and writes the one result line to stdout, the
// reasons to stderr (never a credential).
func run(args []string, getenv func(string) string, stdout, stderr io.Writer, d deps) int {
	flags := flag.NewFlagSet("configbackup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	out := flags.String("out", "", "file to create (must not exist)")
	host := flags.String("host", getenv(envHost), "address of the switch ("+envHost+")")
	macText := flags.String("mac", getenv(envMAC), "MAC address of the switch ("+envMAC+")")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	user, pass := getenv(envUsername), getenv(envPassword)
	switch {
	case *out == "":
		say(stderr, "configbackup: -out FILE is required\n")
	case *host == "" && *macText == "":
		say(stderr, "configbackup: name the switch with -host or -mac (%s, %s)\n", envHost, envMAC)
	case user == "" || pass == "":
		say(stderr, "configbackup: set %s and %s\n", envUsername, envPassword)
	default:
		return backup(backupRequest{out: *out, host: *host, mac: *macText, username: user, password: pass}, stdout, stderr, d)
	}
	return exitUsage
}

// backupRequest is what the command line and the environment ask for.
type backupRequest struct {
	out, host, mac     string // the file to create, the switch's address, its MAC address
	username, password string
}

func backup(req backupRequest, stdout, stderr io.Writer, d deps) int {
	cfg := escp.Config{Host: req.host, Username: req.username, Password: escp.Password(req.password)}
	if req.mac != "" {
		mac, err := escp.ParseMAC(req.mac)
		if err != nil {
			say(stderr, "configbackup: %v\n", err)
			return exitUsage
		}
		cfg.MAC = mac
	}
	client, err := d.dial(cfg)
	if err != nil {
		say(stderr, "configbackup: %v\n", err)
		return exitFailed
	}
	defer func() { _ = client.Close() }() // read-only exchange: a failed close loses nothing
	ctx, cancel := context.WithTimeout(context.Background(), backupLimit)
	defer cancel()
	file, err := client.ConfigBackup(ctx)
	if err != nil {
		say(stderr, "configbackup: no file written: %v\n", err)
		return exitFailed
	}
	if err := save(d.create, req.out, file); err != nil {
		say(stderr, "configbackup: %v\n", err)
		return exitFailed
	}
	say(stdout, "%s %d bytes sha256=%s\n", req.out, file.Size(), file.SHA256())
	return exitOK
}

// save writes the whole file to a new path, or removes what it created and says why.
func save(create func(string) (io.WriteCloser, error), path string, file escp.ConfigFile) error {
	f, err := create(path)
	if err != nil {
		return fmt.Errorf("not saved: %w", err)
	}
	_, werr := f.Write(file)
	if err := errors.Join(werr, f.Close()); err != nil {
		return errors.Join(fmt.Errorf("not saved: %w", err), os.Remove(path))
	}
	return nil
}

// coverage: ignore - process entry point; run holds all the logic and is tested.
func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr, deps{dial: escp.Dial, create: createExclusive}))
}
