// Command popin is the CLI daemon that receives incoming video calls from
// other Popin users and auto-opens a browser tab to join the call.
//
// Subcommands:
//
//	popin login     Authorize the daemon by opening a browser tab to the web
//	                login page. Stores the resulting daemon token on disk.
//	popin signup     Like login, but opens the web page in register mode so a
//	                new account can be created and authorized in one go.
//	popin listen     Connect to the server and listen for incoming calls
//	                 indefinitely, opening a browser tab for each.
//	popin logout     Delete the stored daemon token (and revoke it server-side
//	                 if the server is reachable).
//	popin config     Show or set the server/web URLs (persisted to
//	                 ~/.config/popin/config.json).
//
// URL resolution precedence (highest wins):
//
//  1. --server / --web flags on the current command
//  2. values saved by `popin config` (config.json)
//  3. BACKEND_URL / WEB_URL environment variables (or .env)
//  4. built-in defaults (baked in at build time for release binaries)
//
// With no subcommand, `popin` prints help (like `popin --help`).
//
// Flags (apply to listen/login/signup):
//
//	--server URL   Server base URL (overrides config/env)
//	--web URL      Web app base URL (overrides config/env)
//
// The daemon token is stored at ~/.config/popin/daemon-token (0600). Re-running
// `popin login` replaces any existing daemon session for this user server-side
// (single active daemon per user).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"github.com/popin/popin/internal/cli"
)

func main() {
	// godotenv makes local .env pick up BACKEND_URL/WEB_URL like the server.
	godotenv.Load()

	server := flag.String("server", "", "server base URL (overrides `popin config` / env)")
	web := flag.String("web", "", "web app base URL (overrides `popin config` / env)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: popin [flags] <login|signup|config|listen|logout|friend [username]>\n\n")
		fmt.Fprintf(os.Stderr, "Subcommands:\n  login           authorize the phone attendant via browser\n  signup          register a new account + authorize the phone attendant via browser\n  config          show or set the server/web URLs (persisted to ~/.config/popin/config.json)\n  listen          listen for incoming calls\n  logout          delete the phone attendant token\n  friend [user]   send a friend request to <user>, or open the friends TUI\n                  (accept/deny incoming, unfriend) when no argument given\n\n")
		fmt.Fprintf(os.Stderr, "With no subcommand, prints this help (same as `popin --help`).\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })

	// With no subcommand, show help instead of implicitly starting the
	// listener. Users must opt in with `popin listen`.
	if flag.NArg() == 0 {
		flag.Usage()
		return
	}

	cmd := flag.Arg(0)

	// `popin config` manages the persisted URLs; handle it before resolving
	// URLs for the other commands. It parses its own flags (which appear
	// *after* the subcommand, e.g. `popin config --server X`).
	if cmd == "config" {
		cfgSet := flag.NewFlagSet("config", flag.ExitOnError)
		s := cfgSet.String("server", "", "server base URL")
		w := cfgSet.String("web", "", "web app base URL")
		cfgSet.Usage = func() {
			fmt.Fprintf(os.Stderr, "Usage: popin config [--server URL] [--web URL]\n\n")
			fmt.Fprintf(os.Stderr, "With no flags, prints the currently resolved URLs.\n")
			fmt.Fprintf(os.Stderr, "With flags, saves them to ~/.config/popin/config.json (either flag is optional).\n\n")
			cfgSet.PrintDefaults()
		}
		cfgSet.Parse(flag.Args()[1:])
		cfgSetChanged := map[string]bool{}
		cfgSet.Visit(func(f *flag.Flag) { cfgSetChanged[f.Name] = true })
		if err := cli.RunConfig(*s, *w, cfgSetChanged["server"], cfgSetChanged["web"]); err != nil {
			fmt.Fprintf(os.Stderr, "config failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	cfg, err := cli.Resolve(*server, *web, set["server"], set["web"])
	if err != nil {
		fmt.Fprintf(os.Stderr, "config failed: %v\n", err)
		os.Exit(1)
	}

	switch cmd {
	case "login":
		err = cli.Login(cfg)
	case "signup":
		err = cli.Signup(cfg)
	case "logout":
		err = cli.Logout(cfg)
	case "friend":
		err = cli.Friend(cfg, flag.Args()[1:])
	case "listen":
		err = cli.Listen(cfg)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", cmd)
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		label := cmd
		if cmd == "listen" {
			label = "phone attendant"
		}
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", label, err)
		os.Exit(1)
	}
}
