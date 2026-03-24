package cli

import (
	"fmt"
	"log"

	"github.com/farshidmousavii/netmon/internal/backup"
	"github.com/spf13/cobra"
)

var diffCmd = &cobra.Command{
	Use:   "diff <file1> <file2>",
	Short: "Compare two backup files",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		file1 := args[0]
		file2 := args[1]
		identical, diffs, err := backup.CompareFiles(file1, file2)
		if err != nil {
			log.Fatal(err)
		}

		if identical {
			fmt.Println("Files are identical")
			return
		}

		fmt.Printf("Found %d differences:\n\n", len(diffs))
		for _, diff := range diffs {
			fmt.Printf("Line %d:\n", diff.Line)
			if diff.OldContent != "" {
				fmt.Printf("  - %s\n", diff.OldContent)
			}
			if diff.NewContent != "" {
				fmt.Printf("  + %s\n", diff.NewContent)
			}
			fmt.Println()

		}
	},
}

func init() {
	rootCmd.AddCommand(diffCmd)
}
