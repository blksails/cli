/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/supabase-community/supabase-go"
)

// accessKeyCmd represents the accessKey command. It obtains an authenticated
// Supabase client for the active profile via the shared AuthedClient entry
// point (Requirement 8.1/8.2) instead of assembling the session client inline.
// When AuthedClient cannot return an authenticated client (e.g. the profile is
// not logged in or the session refresh failed → auth.ErrReloginRequired) the
// command surfaces a clear message and exits non-zero via RunE, without
// attempting the query.
var accessKeyCmd = &cobra.Command{
	Use:   "access-key",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := AuthedClient(profile)
		if err != nil {
			return fmt.Errorf("无法获取认证 client: %w", err)
		}
		return runAccessKey(client, cmd.OutOrStdout())
	},
}

// runAccessKey performs the access_keys query against an already-authenticated
// client and writes the raw PostgREST response to w. Taking the client as a
// parameter keeps this seam free of session-assembly concerns: callers (the
// command path) obtain the client via AuthedClient and pass it in.
func runAccessKey(client *supabase.Client, w io.Writer) error {
	out, _, err := client.From("access_keys").Select("", "", false).ExecuteString()
	if err != nil {
		return fmt.Errorf("failed to get access keys: %w", err)
	}
	fmt.Fprintln(w, string(out))
	return nil
}

func init() {
	rootCmd.AddCommand(accessKeyCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// accessKeyCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// accessKeyCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
