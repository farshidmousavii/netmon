package cli

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/farshidmousavii/netmon/internal/config"
	"github.com/farshidmousavii/netmon/internal/device"
	"github.com/farshidmousavii/netmon/internal/logger"
	"github.com/spf13/cobra"
)

var (
	execDevice   string
	execType     string
	execOutput   string
	execCommands []string
	execDryRun   bool
	execSave     bool
)

var execCmd = &cobra.Command{
	Use:   "exec",
	Short: "Execute command(s) on devices",
	Long: `Execute one or more commands on network devices.

Target Selection (choose one):
  -d, --device NAME   Execute on specific device
  --type TYPE         Execute on all devices of type (cisco/mikrotik)

Examples:
  # Single device
  netmon-cli exec -d core-switch -c "show ip interface brief"

  # All Cisco devices with save
  netmon-cli exec --type cisco -c "interface gi0/1" -c "shutdown" --save

  # Dry run (preview without execution)
  netmon-cli exec --type cisco -c "interface gi0/1" -c "shutdown" --dry-run

  # Save output to file
  netmon-cli exec -d core-switch -c "show run" -o output.txt

  # Interactive mode
  netmon-cli exec --type cisco`,
	Run: runExec,
}

func init() {
	rootCmd.AddCommand(execCmd)

	execCmd.Flags().StringVarP(&execDevice, "device", "d", "", "device name or IP")
	execCmd.Flags().StringVar(&execType, "type", "", "device type (cisco/mikrotik)")
	execCmd.Flags().StringVarP(&execOutput, "output", "o", "", "save output to file (e.g., output.txt)")
	execCmd.Flags().StringSliceVarP(&execCommands, "command", "c", []string{}, "command to execute (can be repeated)")
	execCmd.Flags().BoolVar(&execDryRun, "dry-run", false, "preview commands without execution")
	execCmd.Flags().BoolVar(&execSave, "save", false, "save config after execution (Cisco only)")

	execCmd.MarkFlagsMutuallyExclusive("device", "type")
}

func runExec(cmd *cobra.Command, args []string) {
	if err := logger.Init(false); err != nil {
		log.Fatal(err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatal(err)
	}

	// Validation
	if execDevice == "" && execType == "" {
		log.Fatal("Please specify target: -d DEVICE or --type TYPE")
	}

	// Filter devices
	targetDevices := filterDevices(cfg.Devices)
	if len(targetDevices) == 0 {
		log.Fatal("No matching devices found")
	}

	// Interactive mode
	if len(execCommands) == 0 {
		execCommands = readInteractiveCommands(targetDevices)
	}

	if len(execCommands) == 0 {
		log.Fatal("No commands provided")
	}

	// Validate output file extension
	if execOutput != "" {
		ext := filepath.Ext(execOutput)
		if ext != ".txt" && ext != ".log" {
			log.Fatalf("Unsupported file extension: %s (use .txt or .log)", ext)
		}
	}

	// Dry run mode
	if execDryRun {
		printDryRun(targetDevices, execCommands, execSave)
		return
	}

	// Confirmation
	if !confirmExecution(targetDevices, execCommands, execSave) {
		fmt.Println("Execution cancelled")
		return
	}

	logger.Info("Executing %d command(s) on %d device(s)", len(execCommands), len(targetDevices))

	// Execute
	results := executeOnDevices(cfg, targetDevices, execCommands, execSave)

	// Print results
	printExecResults(results)

	// Save to file if requested
	if execOutput != "" {
		if err := saveOutputFile(results, execOutput); err != nil {
			logger.Error("Failed to save output: %v", err)
		} else {
			logger.Info("Output saved to: %s", execOutput)
		}
	}
}

func printDryRun(targetDevices []config.DeviceConfig, commands []string, saveConfig bool) {
	fmt.Println(strings.Repeat("═", 70))
	fmt.Println("           DRY RUN MODE (No execution)")
	fmt.Println(strings.Repeat("═", 70))
	fmt.Printf("\nTarget devices (%d):\n", len(targetDevices))
	for _, d := range targetDevices {
		fmt.Printf("  • %s (%s) - %s\n", d.Name, d.IP, d.Vendor)
	}

	fmt.Println("\nCommands to be executed:")
	for i, cmd := range commands {
		fmt.Printf("  %d. %s\n", i+1, cmd)
	}

	if saveConfig {
		fmt.Println("\n⚠ Config will be saved after execution (Cisco devices only)")
	}

	fmt.Println(strings.Repeat("═", 70))
	fmt.Println("ℹ Run without --dry-run to execute")
}

func filterDevices(devices []config.DeviceConfig) []config.DeviceConfig {
	var filtered []config.DeviceConfig

	for _, d := range devices {
		if execDevice != "" {
			if d.Name == execDevice || d.IP == execDevice {
				filtered = append(filtered, d)
				break
			}
		} else if execType != "" {
			if strings.EqualFold(d.Vendor, execType) {
				filtered = append(filtered, d)
			}
		}
	}

	return filtered
}

func readInteractiveCommands(targetDevices []config.DeviceConfig) []string {
	fmt.Println(strings.Repeat("═", 70))
	fmt.Printf("Target devices (%d):\n", len(targetDevices))
	for _, d := range targetDevices {
		fmt.Printf("  • %s (%s) - %s\n", d.Name, d.IP, d.Vendor)
	}
	fmt.Println(strings.Repeat("═", 70))
	fmt.Println("\nEnter commands (one per line, empty line to finish):")

	scanner := bufio.NewScanner(os.Stdin)
	var commands []string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			break
		}
		commands = append(commands, line)
	}

	return commands
}

func confirmExecution(targetDevices []config.DeviceConfig, commands []string, saveConfig bool) bool {
	fmt.Println(strings.Repeat("═", 70))
	fmt.Printf("⚠ You are about to execute on %d devices:\n", len(targetDevices))
	for _, d := range targetDevices {
		fmt.Printf("  • %s (%s)\n", d.Name, d.IP)
	}
	fmt.Println("\nCommands:")
	for i, cmd := range commands {
		fmt.Printf("  %d. %s\n", i+1, cmd)
	}

	if saveConfig {
		fmt.Println("\n⚠ Config will be saved after execution (Cisco devices only)")
	}

	fmt.Println(strings.Repeat("═", 70))
	fmt.Print("Continue? (yes/no): ")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))

	return answer == "yes" || answer == "y"
}

func executeOnDevices(cfg *config.Config, targetDevices []config.DeviceConfig, commands []string, saveConfig bool) []device.ExecResult {
	results := make(chan device.ExecResult, len(targetDevices))
	var wg sync.WaitGroup

	for _, deviceCfg := range targetDevices {
		wg.Add(1)
		go func(dcfg config.DeviceConfig) {
			defer wg.Done()

			result := device.ExecResult{
				DeviceName: dcfg.Name,
				DeviceIP:   dcfg.IP,
			}

			cred, err := cfg.GetCredential(dcfg.Credential)
			if err != nil {
				result.Error = fmt.Errorf("get credential: %w", err)
				results <- result
				return
			}

			dev, err := device.NewDevice(dcfg, cred)
			if err != nil {
				result.Error = fmt.Errorf("create device: %w", err)
				results <- result
				return
			}

			// execute commands
			output, err := dev.RunCommands(commands)
			if err != nil {
				result.Error = err
				results <- result
				return
			}

			// save flag only for cisco
			if saveConfig && strings.EqualFold(dcfg.Vendor, "cisco") {
				saveOutput, saveErr := dev.SaveConfig()
				if saveErr != nil {
					output += "\n\n[WARN] Failed to save config: " + saveErr.Error()
				} else {
					output += "\n\n" + saveOutput
				}
			}

			result.Output = output
			results <- result
		}(deviceCfg)
	}

	wg.Wait()
	close(results)

	// getting results
	var allResults []device.ExecResult
	for r := range results {
		allResults = append(allResults, r)
	}

	return allResults
}

func printExecResults(results []device.ExecResult) {
	success := 0
	failed := 0

	for i, result := range results {
		fmt.Println()
		fmt.Println(strings.Repeat("═", 70))
		fmt.Printf("Device #%d: %s (%s)\n", i+1, result.DeviceName, result.DeviceIP)
		fmt.Println(strings.Repeat("═", 70))

		if result.Error != nil {
			fmt.Printf("Status: ✗ Failed\n")
			fmt.Printf("Error:  %v\n", result.Error)
			failed++
		} else {
			fmt.Printf("Status: ✓ Success\n")
			fmt.Println(strings.Repeat("─", 70))
			fmt.Println(result.Output)
			success++
		}
	}

	// Summary
	fmt.Println()
	fmt.Println(strings.Repeat("═", 70))
	fmt.Println("Summary:")
	fmt.Printf("  Total:   %d devices\n", len(results))
	fmt.Printf("  Success: %d devices\n", success)
	fmt.Printf("  Failed:  %d devices\n", failed)
	fmt.Println(strings.Repeat("═", 70))
}

func saveOutputFile(results []device.ExecResult, outputPath string) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer file.Close()

	// write header
	fmt.Fprintf(file, "Network Device Execution Report\n")
	fmt.Fprintf(file, "Generated: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(file, "%s\n\n", strings.Repeat("=", 70))

	//write each device
	for i, result := range results {
		fmt.Fprintf(file, "Device #%d: %s (%s)\n", i+1, result.DeviceName, result.DeviceIP)
		fmt.Fprintf(file, "%s\n", strings.Repeat("-", 70))

		if result.Error != nil {
			fmt.Fprintf(file, "Status: FAILED\n")
			fmt.Fprintf(file, "Error: %v\n\n", result.Error)
		} else {
			fmt.Fprintf(file, "Status: SUCCESS\n")
			fmt.Fprintf(file, "%s\n\n", strings.Repeat("-", 70))
			fmt.Fprintf(file, "%s\n\n", result.Output)
		}

		fmt.Fprintf(file, "%s\n\n", strings.Repeat("=", 70))
	}

	return nil
}
