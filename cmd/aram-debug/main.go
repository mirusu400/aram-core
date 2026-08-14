// Command aram-debug drives an ARAM application machine without a GUI.
package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mirusu400/aram-core/application"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/debugkit"
)

type commonOptions struct {
	profile       string
	runBudget     uint64
	width         int
	height        int
	frameDuration time.Duration
	timeout       time.Duration
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	switch args[0] {
	case "lua", "run":
		return runLua(args[1:], stdout, stderr)
	case "serve":
		return runServe(args[1:], stdin, stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "aram-debug: unknown subcommand %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runLua(args []string, stdout io.Writer, stderr io.Writer) int {
	options := defaultOptions()
	flags := newFlagSet("lua", stderr, &options)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: aram-debug lua [flags] APPLICATION SCRIPT.lua")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 2 {
		flags.Usage()
		return 2
	}
	ctx, cancel := commandContext(options.timeout)
	defer cancel()
	session, err := openSession(ctx, flags.Arg(0), options)
	if err != nil {
		fmt.Fprintf(stderr, "aram-debug: %v\n", err)
		return 1
	}
	defer session.Close()
	if err := session.RunLuaFile(ctx, flags.Arg(1), stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aram-debug: %v\n", err)
		return 1
	}
	return 0
}

func runServe(
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) int {
	options := defaultOptions()
	flags := newFlagSet("serve", stderr, &options)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: aram-debug serve [flags] APPLICATION")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return 2
	}
	ctx, cancel := commandContext(options.timeout)
	defer cancel()
	session, err := openSession(ctx, flags.Arg(0), options)
	if err != nil {
		fmt.Fprintf(stderr, "aram-debug: %v\n", err)
		return 1
	}
	defer session.Close()
	if err := session.ServeProtocol(ctx, stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "aram-debug: %v\n", err)
		return 1
	}
	return 0
}

func defaultOptions() commonOptions {
	return commonOptions{
		runBudget:     application.DefaultRunBudget,
		width:         240,
		height:        320,
		frameDuration: debugkit.DefaultFrameDuration,
	}
}

func newFlagSet(
	name string,
	output io.Writer,
	options *commonOptions,
) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(
		&options.profile,
		"profile",
		"",
		"override the application profile ID",
	)
	flags.Uint64Var(
		&options.runBudget,
		"run-budget",
		application.DefaultRunBudget,
		"guest instructions per execution slice",
	)
	flags.IntVar(&options.width, "width", 240, "framebuffer width")
	flags.IntVar(&options.height, "height", 320, "framebuffer height")
	flags.DurationVar(
		&options.frameDuration,
		"frame-duration",
		debugkit.DefaultFrameDuration,
		"virtual duration advanced by each frame",
	)
	flags.DurationVar(
		&options.timeout,
		"timeout",
		0,
		"whole-command timeout (0 disables it)",
	)
	return flags
}

func commandContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(context.Background(), timeout)
	}
	return context.WithCancel(context.Background())
}

func openSession(
	ctx context.Context,
	applicationPath string,
	options commonOptions,
) (*debugkit.Session, error) {
	if options.width <= 0 || options.height <= 0 {
		return nil, fmt.Errorf(
			"invalid framebuffer size %dx%d",
			options.width,
			options.height,
		)
	}
	if options.frameDuration < 0 {
		return nil, fmt.Errorf("frame duration %s is negative", options.frameDuration)
	}
	absolutePath, err := filepath.Abs(applicationPath)
	if err != nil {
		return nil, fmt.Errorf("resolve application path %q: %w", applicationPath, err)
	}
	input, err := os.Open(absolutePath)
	if err != nil {
		return nil, fmt.Errorf("open application %q: %w", absolutePath, err)
	}
	stat, err := input.Stat()
	if err != nil {
		_ = input.Close()
		return nil, fmt.Errorf("stat application %q: %w", absolutePath, err)
	}
	if !stat.Mode().IsRegular() {
		_ = input.Close()
		return nil, fmt.Errorf("application %q is not a regular file", absolutePath)
	}
	factory := application.NewFactory()
	factory.RunBudget = options.runBudget
	factory.FramebufferSize = image.Pt(options.width, options.height)
	machine, createErr := factory.Create(ctx, machinecore.Source{
		Name:      stat.Name(),
		Path:      absolutePath,
		ProfileID: options.profile,
		ReaderAt:  input,
		Size:      stat.Size(),
	})
	closeErr := input.Close()
	if createErr != nil {
		return nil, fmt.Errorf("create machine: %w", createErr)
	}
	if closeErr != nil {
		_ = machine.Close()
		return nil, fmt.Errorf("close application %q: %w", absolutePath, closeErr)
	}
	session, err := debugkit.New(machine, debugkit.Options{
		FrameDuration: options.frameDuration,
		Diagnostics:   application.MachineDiagnostics(machine),
	})
	if err != nil {
		_ = machine.Close()
		return nil, err
	}
	return session, nil
}

func printUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: aram-debug <lua|serve> [flags] ...")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "  lua    run a trusted Lua debugging scenario")
	fmt.Fprintln(output, "  serve  read NDJSON commands from stdin")
}
