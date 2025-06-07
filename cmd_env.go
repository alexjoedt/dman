package dman

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
)

type envCommand struct {
	name  string
	usage string
}

func EnvCommand() *envCommand {
	return &envCommand{
		name:  "env",
		usage: "manage environments (git branches)",
	}
}

func (e *envCommand) Name() string {
	return e.name
}

func (e *envCommand) Usage() string {
	return e.usage
}

func (e *envCommand) Flags() []cli.Flag {
	return []cli.Flag{}
}

func (e *envCommand) Action(ctx context.Context, c *cli.Command) error {
	args := c.Args()
	if args.Len() == 0 {
		return fmt.Errorf("env command requires a subcommand: list, switch, create, current")
	}

	subcommand := args.Get(0)

	switch subcommand {
	case "list":
		return e.listEnvironments(ctx)
	case "switch":
		if args.Len() < 2 {
			return fmt.Errorf("switch requires an environment name")
		}
		return e.switchEnvironment(ctx, args.Get(1))
	case "create":
		if args.Len() < 2 {
			return fmt.Errorf("create requires an environment name")
		}
		return e.createEnvironment(ctx, args.Get(1))
	case "current":
		return e.currentEnvironment(ctx)
	default:
		return fmt.Errorf("unknown subcommand: %s", subcommand)
	}
}

func (e *envCommand) listEnvironments(ctx context.Context) error {
	config, err := readConfig()
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	repo, err := getRepo(config.Path)
	if err != nil {
		return fmt.Errorf("failed to initialize repo: %w", err)
	}

	branches, currentBranch, err := repo.ListBranches(ctx)
	if err != nil {
		return fmt.Errorf("failed to list branches: %w", err)
	}

	fmt.Println("Available environments:")
	for _, branch := range branches {
		if branch == currentBranch {
			fmt.Printf("* %s (current)\n", branch)
		} else {
			fmt.Printf("  %s\n", branch)
		}
	}

	return nil
}

func (e *envCommand) switchEnvironment(ctx context.Context, envName string) error {
	config, err := readConfig()
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	repo, err := getRepo(config.Path)
	if err != nil {
		return fmt.Errorf("failed to initialize repo: %w", err)
	}

	// Check if branch exists
	branches, _, err := repo.ListBranches(ctx)
	if err != nil {
		return fmt.Errorf("failed to list branches: %w", err)
	}

	branchExists := false
	for _, branch := range branches {
		if branch == envName {
			branchExists = true
			break
		}
	}

	if !branchExists {
		return fmt.Errorf("environment '%s' does not exist. Use 'dman env create %s' to create it", envName, envName)
	}

	// Switch to the branch
	if err := repo.Checkout(ctx, envName); err != nil {
		return fmt.Errorf("failed to switch to environment '%s': %w", envName, err)
	}

	// Update config with new branch
	config.Branch = envName
	if err := saveConfig(config); err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	fmt.Printf("Switched to environment: %s\n", envName)
	return nil
}

func (e *envCommand) createEnvironment(ctx context.Context, envName string) error {
	config, err := readConfig()
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	repo, err := getRepo(config.Path)
	if err != nil {
		return fmt.Errorf("failed to initialize repo: %w", err)
	}

	// Check if branch already exists
	branches, _, err := repo.ListBranches(ctx)
	if err != nil {
		return fmt.Errorf("failed to list branches: %w", err)
	}

	for _, branch := range branches {
		if branch == envName {
			return fmt.Errorf("environment '%s' already exists. Use 'dman env switch %s' to switch to it", envName, envName)
		}
	}

	// Create and switch to new branch
	if err := repo.CheckoutNewBranch(ctx, envName); err != nil {
		return fmt.Errorf("failed to create environment '%s': %w", envName, err)
	}

	// Update config with new branch
	config.Branch = envName
	if err := saveConfig(config); err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	fmt.Printf("Created and switched to new environment: %s\n", envName)
	return nil
}

func (e *envCommand) currentEnvironment(ctx context.Context) error {
	config, err := readConfig()
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	repo, err := getRepo(config.Path)
	if err != nil {
		return fmt.Errorf("failed to initialize repo: %w", err)
	}

	currentBranch, err := repo.CurrentBranch(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current branch: %w", err)
	}

	fmt.Printf("Current environment: %s\n", currentBranch)
	return nil
}
